// Package maintenance prévient les joueurs avant un redémarrage du backend,
// bascule le MOTD en compte à rebours, puis les déconnecte proprement en leur
// donnant l'heure de retour.
//
// Déclenchement : un signal POSIX envoyé au conteneur Gate par l'hôte
// (« podman kill --signal USR1 gate »), parce que le proxy n'a ni console ni
// port d'administration et que le déclencheur est un script de l'hôte
// (mc-maintenance.sh, dnf-automatic, podman auto-update) :
//
//	SIGUSR1 — mise à jour du logiciel dans le conteneur (Minecraft, mods)
//	SIGUSR2 — mise à jour du serveur principal (l'hôte, l'OS)
//
// Le même signal renvoyé pendant une fenêtre l'annule (mise à jour avortée) ;
// renvoyé après l'échéance, il lève l'état de maintenance. SIGINT et SIGTERM
// restent à Gate (pkg/util/interrupt), on ne touche qu'à USR1/USR2.
package maintenance

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/robinbraemer/event"
	"go.minekube.com/common/minecraft/color"
	"go.minekube.com/common/minecraft/component"
	"go.minekube.com/gate/pkg/edition/java/proxy"
	"golang.org/x/text/language"
)

// Raison est le motif du redémarrage. C'est une valeur typée et PAS du texte
// libre : elle sert de clé dans la table de traduction, sinon un joueur thaï
// recevrait la raison en français.
type Raison string

const (
	RaisonConteneur Raison = "raison.conteneur" // mise à jour du jeu dans le conteneur
	RaisonHôte      Raison = "raison.hote"      // mise à jour de l'hôte / de l'OS
	raisonInconnue  Raison = "raison.inconnue"  // repli : jamais de clé brute affichée
)

// Window décrit un redémarrage planifié du backend.
type Window struct {
	StartsAt time.Time
	Raison   Raison
}

// Warnings est le compte à rebours diffusé avant StartsAt. Le premier palier
// vaut aussi le préavis : une fenêtre ouverte avec des joueurs présents dure
// le plus grand des paliers.
var Warnings = []time.Duration{5 * time.Minute, 2 * time.Minute, time.Minute}

// RetourAprès est l'estimation annoncée au joueur (« retour vers 21:05 »).
// C'est un bouton de calibration : un redémarrage réel dépend de la machine,
// de la taille du monde et du nombre de mods. À ajuster sur la mesure.
var RetourAprès = 5 * time.Minute

// La mise en veille et la fenêtre de maintenance sont le MÊME problème : dans
// les deux cas le backend est absent et Gate tient la connexion. La différence
// tient à ce qui met fin à l'absence — un redémarrage terminé, ou un joueur qui
// frappe à la porte. D'où un seul chemin de code, et handleDuringOutage au
// milieu des deux.
//
// SleepWhenEmpty ne doit PAS être activé sur un compte Oracle Always Free :
// Oracle récupère les instances inactives (CPU, réseau et mémoire tous sous
// 20 % sur 7 jours). Voir ansible/group_vars/all/main.yml.
var (
	SleepWhenEmpty bool
	SleepAfter     = 20 * time.Minute
)

// langueDéfaut est le repli quand la locale du client est inconnue de la table.
const langueDéfaut = "en"

// messages : langue → clé → texte. Ajouter une langue = ajouter une entrée
// ici, rien d'autre. Aucune dépendance i18n, aucun fichier de ressources : la
// table tient en une page et le test vérifie qu'aucune combinaison ne manque.
//
// Messages volontairement COURTS : le thaï n'a pas d'espaces entre les mots et
// le retour à la ligne automatique du client traite mal les phrases longues.
// Les durées et les heures restent en chiffres (« 5 min », « 21:05 ») : c'est
// lisible dans les cinq langues et ça évite une table de pluriels.
var messages = map[string]map[string]string{
	"fr": {
		"rappel":                "Redémarrage dans %s — %s",
		"deconnexion":           "Redémarrage — %s\nRetour vers %s",
		"annulation":            "Redémarrage annulé",
		string(RaisonConteneur): "mise à jour du jeu",
		string(RaisonHôte):      "mise à jour du serveur",
		string(raisonInconnue):  "entretien",
	},
	"en": {
		"rappel":                "Restart in %s — %s",
		"deconnexion":           "Restart — %s\nBack around %s",
		"annulation":            "Restart cancelled",
		string(RaisonConteneur): "game update",
		string(RaisonHôte):      "server update",
		string(raisonInconnue):  "maintenance",
	},
	"es": {
		"rappel":                "Reinicio en %s — %s",
		"deconnexion":           "Reinicio — %s\nVolvemos hacia las %s",
		"annulation":            "Reinicio cancelado",
		string(RaisonConteneur): "actualización del juego",
		string(RaisonHôte):      "actualización del servidor",
		string(raisonInconnue):  "mantenimiento",
	},
	// th et pt : traductions NON relues par un locuteur natif.
	"th": {
		"rappel":                "รีสตาร์ทใน %s — %s",
		"deconnexion":           "รีสตาร์ท — %s\nกลับมา %s",
		"annulation":            "ยกเลิกการรีสตาร์ท",
		string(RaisonConteneur): "อัปเดตเกม",
		string(RaisonHôte):      "อัปเดตเซิร์ฟเวอร์",
		string(raisonInconnue):  "ปิดปรับปรุง",
	},
	"pt": {
		"rappel":                "Reinício em %s — %s",
		"deconnexion":           "Reinício — %s\nVoltamos por volta das %s",
		"annulation":            "Reinício cancelado",
		string(RaisonConteneur): "atualização do jogo",
		string(RaisonHôte):      "atualização do servidor",
		string(raisonInconnue):  "manutenção",
	},
}

// langue ramène une locale de client à une langue de la table.
// Le sous-tag de base suffit (pt-BR → pt, th-TH → th) ; on ne découpe pas la
// chaîne à la main, x/text sait le faire.
func langue(tag language.Tag) string {
	base, _ := tag.Base()
	if _, ok := messages[base.String()]; ok {
		return base.String()
	}
	return langueDéfaut
}

// texte rend un message traduit. Une clé absente d'une langue retombe sur
// l'anglais plutôt que d'afficher la clé brute au joueur.
func texte(lg, clé string) string {
	if t, ok := messages[lg][clé]; ok {
		return t
	}
	return messages[langueDéfaut][clé]
}

// raisonTexte traduit la raison ; une raison inconnue devient « entretien ».
func raisonTexte(lg string, r Raison) string {
	if _, ok := messages[langueDéfaut][string(r)]; !ok {
		r = raisonInconnue
	}
	return texte(lg, string(r))
}

// minutes arrondit au-dessus : il reste « 1 min » tant qu'il reste quelque
// chose, jamais « 0 min ».
func minutes(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int((d + time.Minute - 1) / time.Minute)
}

func durée(d time.Duration) string { return fmt.Sprintf("%d min", minutes(d)) }

// paliers garde les paliers encore atteignables dans le temps restant, du plus
// lointain au plus proche. Warnings n'est pas supposé trié et peut contenir un
// palier plus grand que le préavis (fenêtre raccourcie) : ces paliers-là sont
// simplement sautés.
func paliers(warnings []time.Duration, restant time.Duration) []time.Duration {
	out := make([]time.Duration, 0, len(warnings))
	for _, d := range warnings {
		if d > 0 && d <= restant {
			out = append(out, d)
		}
	}
	// ponytail : tri par insertion sur trois éléments, slices.Sort ferait le
	// même travail en important un paquet de plus pour trois valeurs.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] > out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// préavis est la durée d'une fenêtre ouverte avec des joueurs présents.
func préavis() time.Duration {
	var max time.Duration
	for _, d := range Warnings {
		if d > max {
			max = d
		}
	}
	return max
}

type plugin struct {
	proxy *proxy.Proxy
	log   logr.Logger

	// mu protège l'état de la fenêtre : le compte à rebours tourne dans une
	// goroutine pendant que le PingEvent est servi en parallèle.
	mu      sync.Mutex
	fenêtre *Window
	annuler context.CancelFunc
}

// Register branche le plugin sur le proxy.
func Register() {
	proxy.Plugins = append(proxy.Plugins, proxy.Plugin{
		Name: "maintenance",
		Init: func(ctx context.Context, p *proxy.Proxy) error {
			pl := &plugin{proxy: p, log: logr.FromContextOrDiscard(ctx).WithName("maintenance")}

			event.Subscribe(p.Event(), 0, pl.auPing)
			event.Subscribe(p.Event(), 0, pl.auPostLogin)
			go pl.écouterSignaux(ctx)

			pl.log.Info("maintenance active", "préavis", préavis(),
				"paliers", Warnings, "veilleSiVide", SleepWhenEmpty, "veilleAprès", SleepAfter)
			return nil
		},
	})
}

// écouterSignaux traduit SIGUSR1/SIGUSR2 en fenêtres de maintenance.
func (pl *plugin) écouterSignaux(ctx context.Context) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGUSR1, syscall.SIGUSR2)
	defer signal.Stop(sig)

	for {
		select {
		case <-ctx.Done():
			return
		case s := <-sig:
			raison := RaisonConteneur
			if s == syscall.SIGUSR2 {
				raison = RaisonHôte
			}
			pl.basculer(ctx, raison)
		}
	}
}

// basculer ouvre une fenêtre, ou referme celle qui est en cours.
//
// Un second signal pendant le compte à rebours annule la fenêtre (mise à jour
// avortée) ; après l'échéance, il lève l'état de maintenance — le MOTD redevient
// normal. C'est le même geste côté opérateur, d'où le même signal.
func (pl *plugin) basculer(ctx context.Context, r Raison) {
	pl.mu.Lock()
	if w := pl.fenêtre; w != nil {
		annuler := pl.annuler
		commencée := !time.Now().Before(w.StartsAt)
		pl.fenêtre, pl.annuler = nil, nil
		pl.mu.Unlock()

		if annuler != nil {
			annuler()
		}
		if commencée {
			pl.log.Info("maintenance levée")
		} else {
			pl.log.Info("fenêtre annulée", "raison", w.Raison)
			pl.diffuser(func(lg string) string { return texte(lg, "annulation") })
		}
		return
	}

	// Personne en ligne : rien à prévenir, on applique tout de suite.
	attente := time.Duration(0)
	if pl.proxy.PlayerCount() > 0 {
		attente = préavis()
	}
	w := Window{StartsAt: time.Now().Add(attente), Raison: r}
	ctx, annuler := context.WithCancel(ctx)
	pl.fenêtre, pl.annuler = &w, annuler
	pl.mu.Unlock()

	pl.log.Info("fenêtre ouverte", "raison", r, "échéance", w.StartsAt,
		"joueurs", pl.proxy.PlayerCount())
	go pl.compteÀRebours(ctx, w)
}

// état rend la fenêtre en cours, s'il y en a une.
func (pl *plugin) état() (Window, bool) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.fenêtre == nil {
		return Window{}, false
	}
	return *pl.fenêtre, true
}

// compteÀRebours diffuse les rappels puis applique la fenêtre à l'échéance.
func (pl *plugin) compteÀRebours(ctx context.Context, w Window) {
	for _, d := range paliers(Warnings, time.Until(w.StartsAt)) {
		if !dormir(ctx, time.Until(w.StartsAt.Add(-d))) {
			return
		}
		pl.diffuser(func(lg string) string {
			return fmt.Sprintf(texte(lg, "rappel"), durée(d), raisonTexte(lg, w.Raison))
		})
	}
	if !dormir(ctx, time.Until(w.StartsAt)) {
		return
	}

	// La fenêtre reste posée après l'échéance : le MOTD doit rester en
	// maintenance tant que le backend est absent.
	pl.log.Info("échéance atteinte", "raison", w.Raison, "joueurs", pl.proxy.PlayerCount())
	for _, j := range pl.proxy.Players() {
		handleDuringOutage(j, w)
	}
}

// dormir attend d, ou rend false si la fenêtre est annulée entre-temps.
func dormir(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// diffuser envoie un message à tous les joueurs, chacun dans sa langue.
func (pl *plugin) diffuser(rendre func(lg string) string) {
	for _, j := range pl.proxy.Players() {
		envoyer(j, rendre(langue(j.Settings().Locale())))
	}
}

// envoyer écrit un message au joueur. La locale est lue ICI et pas au login :
// tant que le paquet ClientSettings n'est pas arrivé, Settings() rend
// player.DefaultSettings (en_US) — au login la réponse est donc systématiquement
// fausse pour un joueur non anglophone.
func envoyer(j proxy.Player, texte string) {
	_ = j.SendMessage(&component.Text{Content: texte, S: component.Style{Color: color.Yellow}})
}

// handleDuringOutage décide de ce qui arrive à un joueur connecté quand le
// backend Minecraft s'absente : déconnexion propre, avec la raison et l'heure
// de retour.
//
// Écartées : la salle d'attente (Gate garde la connexion TCP) exige un backend
// « limbo » minimal, sans quoi le client reste figé sur un écran vide — un
// service de plus à opérer sur 12 Go ; la file d'attente ordonnée n'évite une
// ruée de reconnexions que s'il y a une ruée, et la communauté se compte en
// dizaines de joueurs. Les deux se rajoutent le jour où la mesure les
// justifie ; l'heure de retour affichée coûte une ligne.
func handleDuringOutage(j proxy.Player, w Window) {
	lg := langue(j.Settings().Locale())
	retour := time.Now().Add(RetourAprès).Format("15:04")
	j.Disconnect(&component.Text{
		Content: fmt.Sprintf(texte(lg, "deconnexion"), raisonTexte(lg, w.Raison), retour),
		S:       component.Style{Color: color.Yellow},
	})
}

// auPing remplace le MOTD par le compte à rebours.
//
// Le ping de la liste des serveurs ne transporte AUCUNE locale : impossible de
// le localiser. D'où un libellé chiffré, avec un mot anglais court — un nombre
// de minutes se lit dans les cinq langues.
func (pl *plugin) auPing(e *proxy.PingEvent) {
	w, ok := pl.état()
	if !ok {
		return
	}
	p := e.Ping()
	if p == nil {
		return
	}
	txt := "⚠ Maintenance"
	if restant := time.Until(w.StartsAt); restant > 0 {
		txt = fmt.Sprintf("⚠ Maintenance — %d min", minutes(restant))
	}
	p.Description = &component.Text{Content: txt, S: component.Style{Color: color.Yellow}}
	e.SetPing(p)
}

// délaiRappelArrivée laisse au client le temps d'envoyer ses ClientSettings,
// sans quoi on lirait en_US pour tout le monde.
var délaiRappelArrivée = 3 * time.Second

// auPostLogin prévient le joueur qui arrive pendant le compte à rebours. Il
// est accepté : il a vu le MOTD, et refuser à l'entrée coûterait plus qu'un
// message.
func (pl *plugin) auPostLogin(e *proxy.PostLoginEvent) {
	if _, ok := pl.état(); !ok {
		return
	}
	j := e.Player()
	go func() {
		if !dormir(j.Context(), délaiRappelArrivée) {
			return
		}
		w, ok := pl.état()
		if !ok {
			return
		}
		lg := langue(j.Settings().Locale())
		restant := time.Until(w.StartsAt)
		if restant <= 0 {
			return // l'échéance est passée, la déconnexion s'en charge
		}
		envoyer(j, fmt.Sprintf(texte(lg, "rappel"), durée(restant), raisonTexte(lg, w.Raison)))
	}()
}
