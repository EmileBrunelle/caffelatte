// Package invitation tient la liste d'autorisation du serveur DANS Gate, et
// refuse à la connexion tout joueur qui n'y figure pas. Un parrain ajoute un
// ami depuis le jeu avec « /invite <pseudo> », sans SSH ni console.
//
// Trois commandes, une seule liste :
//   - /invite <pseudo Java>       — résolution par l'API Mojang
//   - /invite-bedrock <gamertag>  — résolution par l'API GeyserMC, puis calcul
//     de l'UUID Floodgate avec le code de Gate lui-même (voir bedrock.go).
//     L'API Mojang ne connaît que les comptes Java : sans ce chemin, un ami
//     qui ne joue que sur téléphone ne peut pas être invité du tout.
//   - /uninvite <cible>           — révocation, et expulsion si la cible est
//     en ligne (voir révoquer()).
//
// Pourquoi Gate et pas la liste blanche vanilla du backend : le réseau podman
// « mc » est Internal=true, le backend n'a donc AUCUN accès sortant. Or
// « whitelist add » en vanilla résout le profil auprès de l'API Mojang — un
// pseudo absent du cache local échoue systématiquement. Gate, lui, a l'egress
// (il fait déjà l'authentification Mojang, onlineMode: true) et connaît donc le
// vrai UUID. Et comme le backend n'est joignable QUE par Gate (réseau interne,
// aucun port publié), filtrer dans Gate suffit : il n'existe pas de chemin qui
// contourne le proxy. La liste blanche vanilla est donc désactivée côté backend
// (WHITELIST=false, voir containers/minecraft/entrypoint.sh) ; la garder active
// rejetterait les invités que le backend ne sait pas résoudre.
//
// C'est une commande proxy « /invite <pseudo> » et PAS un mot-clé de chat
// « !invite ». Raison, vérifiée dans la source de Gate v0.74.0 :
// pkg/edition/java/proxy/handle_chat.go annule un PlayerChatEvent en appelant
// invalidCancel() → disconnectIllegalProtocolState(), qui DÉCONNECTE le joueur
// dès que le message est signé (Minecraft 1.19.1+) et que forceKeyAuthentication
// est actif — et il l'est par défaut (pkg/edition/java/config/config.go:83).
// Intercepter le chat coûterait donc soit l'expulsion du parrain, soit la
// désactivation de l'authentification par clé. Une commande enregistrée dans le
// gestionnaire de Gate, elle, est consommée par le proxy et n'atteint jamais le
// backend (handle_cmd.go, consumeCommand) sans toucher à la chaîne de signature.
package invitation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/robinbraemer/event"
	"go.minekube.com/brigodier"
	"go.minekube.com/common/minecraft/color"
	"go.minekube.com/common/minecraft/component"
	"go.minekube.com/gate/pkg/command"
	"go.minekube.com/gate/pkg/edition/java/proxy"
	"go.minekube.com/gate/pkg/util/uuid"
)

// pseudoValide est la frontière de confiance. Le pseudo part dans une URL vers
// l'API Mojang et finit dans un fichier JSON : on refuse tout ce qui n'est pas
// exactement un pseudo Minecraft, on n'« assainit » rien. Règle Mojang :
// 3 à 16 caractères, [A-Za-z0-9_].
// En Go, sans le drapeau (?m), « $ » ne matche qu'en fin de texte — un
// « toto\nop » est donc bien refusé.
var pseudoValide = regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`)

type config struct {
	parrains map[uuid.UUID]bool // autorisation par UUID : les pseudos changent, pas les UUID
	admins   map[uuid.UUID]bool // sous-ensemble autorisé à révoquer n'importe qui
	plafond  int
	chemin   string // fichier JSON de la liste d'autorisation
}

// chargerConfig lit la configuration depuis l'environnement.
//
// Pourquoi pas config.yml : vérifié dans pkg/gate/gate.go, fixedReadInConfig()
// désérialise le fichier dans config.Config puis ré-injecte le résultat dans
// viper. Toute clé inconnue de Gate est perdue en chemin — un plugin embarqué
// ne peut rien lire du fichier. Gate expose en revanche GATE_*/env, et c'est la
// convention qu'on suit ici.
func chargerConfig() (*config, error) {
	c := &config{
		parrains: map[uuid.UUID]bool{},
		admins:   map[uuid.UUID]bool{},
		plafond:  5,
		chemin:   envDefaut("CAFFELATTE_LISTE", "/var/lib/gate/invitations.json"),
	}

	var err error
	if c.parrains, err = ensembleUUID("CAFFELATTE_PARRAINS"); err != nil {
		return nil, err
	}
	if len(c.parrains) == 0 {
		return nil, errors.New("CAFFELATTE_PARRAINS est vide : aucun parrain autorisé")
	}
	// Les administrateurs peuvent révoquer n'importe quelle entrée, y compris
	// celle d'un parrain retiré de la configuration. Vide par défaut : sans eux
	// chacun ne révoque que ses propres invités.
	if c.admins, err = ensembleUUID("CAFFELATTE_ADMINS"); err != nil {
		return nil, err
	}

	if s := os.Getenv("CAFFELATTE_PLAFOND_INVITATIONS"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("CAFFELATTE_PLAFOND_INVITATIONS : %q n'est pas un entier positif", s)
		}
		c.plafond = n
	}

	return c, nil
}

// ensembleUUID lit une variable d'environnement « uuid,uuid,... ». Une valeur
// mal formée est une erreur de configuration, jamais une entrée ignorée en
// silence : une liste d'autorisation amputée sans bruit est un trou.
func ensembleUUID(clé string) (map[uuid.UUID]bool, error) {
	m := map[uuid.UUID]bool{}
	for _, s := range strings.Split(os.Getenv(clé), ",") {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("%s : %q n'est pas un UUID", clé, s)
		}
		if id == uuid.Nil {
			return nil, fmt.Errorf("%s : l'UUID nul n'est pas une identité", clé)
		}
		m[id] = true
	}
	return m, nil
}

func envDefaut(clé, défaut string) string {
	if v := os.Getenv(clé); v != "" {
		return v
	}
	return défaut
}

type plugin struct {
	cfg   *config
	liste *liste
	log   logr.Logger

	// résoudre* : cible → (UUID, étiquette). Champs plutôt qu'appels directs
	// pour que le test injecte une API simulée sans toucher au réseau.
	résoudreJava    func(ctx context.Context, pseudo string) (uuid.UUID, string, error)
	résoudreBedrock func(ctx context.Context, gamertag string) (uuid.UUID, string, error)

	// expulser déconnecte un joueur déjà en ligne. Nil en test.
	expulser func(id uuid.UUID, motif string)
}

// Register branche la commande et le filtre de connexion sur le proxy.
//
// Échec fermé, et cette fois jusqu'au bout : une erreur de configuration
// renvoie une erreur d'init, ce qui annule le démarrage du proxy (voir
// proxy.Plugin). C'est délibérément plus strict qu'avant : ce plugin N'EST PLUS
// un simple raccourci d'administration, il EST le contrôle d'accès du serveur.
// Ne pas s'enregistrer laisserait le serveur grand ouvert. Un proxy qui refuse
// de démarrer est visible ; un proxy qui démarre sans filtre ne l'est pas.
func Register() {
	proxy.Plugins = append(proxy.Plugins, proxy.Plugin{
		Name: "invitation",
		Init: func(ctx context.Context, p *proxy.Proxy) error {
			log := logr.FromContextOrDiscard(ctx).WithName("invitation")

			cfg, err := chargerConfig()
			if err != nil {
				return fmt.Errorf("configuration des invitations invalide : %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(cfg.chemin), 0o700); err != nil {
				return fmt.Errorf("répertoire de la liste d'invitations : %w", err)
			}
			l, err := chargerListe(cfg.chemin)
			if err != nil {
				return fmt.Errorf("liste d'invitations : %w", err)
			}

			pl := &plugin{
				cfg: cfg, liste: l, log: log,
				résoudreJava:    résoudreMojang,
				résoudreBedrock: résoudreGeyser,
				// Disconnect ferme la connexion avec un message affiché :
				// pkg/edition/java/proxy/player.go:606, (*connectedPlayer).Disconnect.
				// Player(id) cherche parmi les joueurs connectés
				// (proxy.go:857) et renvoie nil si personne ne l'est.
				expulser: func(id uuid.UUID, motif string) {
					if j := p.Player(id); j != nil {
						j.Disconnect(&component.Text{
							Content: motif,
							S:       component.Style{Color: color.Red},
						})
					}
				},
			}

			p.Command().Register(brigodier.Literal("invite").
				Then(brigodier.Argument("pseudo", brigodier.StringWord).
					Executes(command.Command(func(c *command.Context) error {
						return pl.inviter(c, c.Source, c.String("pseudo"), false)
					}))).
				Executes(command.Command(func(c *command.Context) error {
					return répondre(c.Source, color.Yellow, "Usage : /invite <pseudo Java>")
				})))

			// Commande séparée plutôt que détection automatique : un gamertag
			// Xbox et un pseudo Java peuvent être identiques, aucune heuristique
			// ne peut trancher lequel des deux le parrain visait — et se tromper
			// d'annuaire inscrit le mauvais UUID. StringPhrase (gourmand) et non
			// StringWord : un gamertag contient des espaces.
			p.Command().Register(brigodier.Literal("invite-bedrock").
				Then(brigodier.Argument("gamertag", brigodier.StringPhrase).
					Executes(command.Command(func(c *command.Context) error {
						return pl.inviter(c, c.Source, c.String("gamertag"), true)
					}))).
				Executes(command.Command(func(c *command.Context) error {
					return répondre(c.Source, color.Yellow, "Usage : /invite-bedrock <gamertag Xbox>")
				})))

			p.Command().Register(brigodier.Literal("uninvite").
				Then(brigodier.Argument("cible", brigodier.StringPhrase).
					Executes(command.Command(func(c *command.Context) error {
						return pl.révoquer(c.Source, c.String("cible"))
					}))).
				Executes(command.Command(func(c *command.Context) error {
					return répondre(c.Source, color.Yellow, "Usage : /uninvite <pseudo, gamertag ou UUID>")
				})))

			// LoginEvent et pas PreLoginEvent : à PreLogin, Gate n'a encore ni
			// authentifié le joueur ni laissé Geyser réécrire son profil. Le
			// profil définitif est posé au GameProfileRequestEvent (fired dans
			// authSessionHandler.Activated, pkg/edition/java/proxy/session_client_auth.go:93)
			// et c'est là que l'intégration Geyser de Gate applique l'identité
			// Floodgate (pkg/edition/bedrock/geyser/geyser.go:379, onGameProfile).
			// LoginEvent est déclenché ensuite, dans
			// completeLoginProtocolPhaseAndInitialize (session_client_auth.go:182),
			// avant l'envoi de LoginSuccess et avant registerConnection : on y
			// voit l'UUID final, Java comme Bedrock, et Deny() coupe la
			// connexion avec un message.
			event.Subscribe(p.Event(), 0, pl.auLogin)

			log.Info("invitations actives", "parrains", len(cfg.parrains),
				"admins", len(cfg.admins), "plafond", cfg.plafond, "liste", cfg.chemin)
			return nil
		},
	})
}

// auLogin refuse tout joueur absent de la liste. Les parrains sont autorisés
// d'office : ce sont eux qui distribuent les invitations, les inscrire en plus
// dans le fichier serait une occasion de les en sortir par erreur.
func (p *plugin) auLogin(e *proxy.LoginEvent) {
	j := e.Player()
	if p.autorisé(j.ID()) {
		return
	}
	p.log.Info("connexion refusée", "uuid", j.ID(), "pseudo", j.Username(),
		"horodatage", time.Now().UTC(), "raison", "absent de la liste d'invitation")
	e.Deny(&component.Text{
		Content: "Ce serveur est sur invitation.\nDemande à un joueur déjà présent de taper /invite " + j.Username() + ".",
		S:       component.Style{Color: color.Red},
	})
}

// autorisé est la décision de laisser entrer, isolée de l'événement pour être
// testable : les champs de proxy.LoginEvent ne sont pas exportés.
func (p *plugin) autorisé(id uuid.UUID) bool {
	return p.cfg.parrains[id] || p.liste.contient(id)
}

// inviter inscrit une cible dans la liste. Deux chemins de résolution (Java
// via Mojang, Bedrock via GeyserMC), une seule liste, un seul plafond, un seul
// journal — c'est tout l'intérêt de partager cette fonction.
func (p *plugin) inviter(ctx context.Context, src command.Source, cible string, bedrock bool) error {
	valide, résoudre, annuaire := pseudoValide.MatchString, p.résoudreJava, "Mojang"
	if bedrock {
		valide, résoudre, annuaire = gamertagValide, p.résoudreBedrock, "GeyserMC"
	}

	joueur, ok := src.(proxy.Player)
	if !ok {
		return p.refuser(src, uuid.Nil, "console", cible, "la source n'est pas un joueur")
	}

	// Autorisation par UUID authentifié (onlineMode: true côté Gate), jamais
	// par pseudo.
	if !p.cfg.parrains[joueur.ID()] {
		return p.refuser(src, joueur.ID(), joueur.Username(), cible, "parrain non autorisé")
	}

	if !valide(cible) {
		// On ne renvoie pas la cible refusée au joueur : inutile, et ça évite
		// de lui réafficher sa propre charge utile.
		return p.refuser(src, joueur.ID(), joueur.Username(), cible, "nom invalide")
	}

	id, canonique, err := résoudre(ctx, cible)
	switch {
	case errors.Is(err, errPseudoInconnu):
		return p.refuser(src, joueur.ID(), joueur.Username(), cible, "ce pseudo n'existe pas chez Mojang")
	case errors.Is(err, errGamertagInconnu):
		return p.refuser(src, joueur.ID(), joueur.Username(), cible,
			"ce gamertag est introuvable (jamais passé par Geyser, ou service indisponible)")
	case err != nil:
		// Échec fermé : dans le doute sur l'identité, on n'inscrit rien.
		p.log.Error(err, "invitation refusée", "parrainUUID", joueur.ID(), "parrain", joueur.Username(),
			"invité", cible, "horodatage", time.Now().UTC(), "résultat", "refusée", "raison", annuaire+" injoignable")
		return répondre(src, color.Red, annuaire+" est injoignable, invitation refusée. Réessaie plus tard.")
	}

	err = p.liste.ajouter(entrée{
		UUID:    id,
		Pseudo:  canonique,
		Parrain: joueur.ID(),
		Date:    time.Now().UTC(),
	}, p.cfg.plafond)
	switch {
	case errors.Is(err, errDéjàAutorisé):
		return répondre(src, color.Yellow, canonique+" est déjà autorisé.")
	case errors.Is(err, errPlafond):
		return p.refuser(src, joueur.ID(), joueur.Username(), cible, "plafond d'invitations atteint")
	case err != nil:
		p.log.Error(err, "invitation refusée", "parrainUUID", joueur.ID(), "parrain", joueur.Username(),
			"invité", cible, "horodatage", time.Now().UTC(), "résultat", "refusée", "raison", "écriture de la liste")
		return répondre(src, color.Red, "Impossible d'enregistrer l'invitation, elle n'est PAS prise en compte.")
	}

	p.log.Info("invitation acceptée", "parrainUUID", joueur.ID(), "parrain", joueur.Username(),
		"invitéUUID", id, "invité", canonique, "édition", éditionDe(bedrock),
		"horodatage", time.Now().UTC(), "résultat", "acceptée")
	return répondre(src, color.Green, canonique+" peut maintenant se connecter.")
}

func éditionDe(bedrock bool) string {
	if bedrock {
		return "Bedrock"
	}
	return "Java"
}

// révoquer retire une entrée de la liste et expulse la cible si elle est en
// ligne.
//
// Modèle d'autorisation : un parrain ne retire que les invités qu'il a
// lui-même inscrits ; un administrateur (CAFFELATTE_ADMINS) retire n'importe
// qui. Pourquoi deux niveaux plutôt que « tout parrain retire tout » : le
// plafond est déjà par parrain, chacun est donc responsable de ses invités, et
// on évite qu'un compte de parrain compromis vide la liste des autres. Et
// pourquoi des administrateurs quand même : sortir un parrain de
// CAFFELATTE_PARRAINS N'INVALIDE PAS ses invités — ils restent autorisés, et
// c'est délibéré (retirer un parrain ne doit pas éjecter silencieusement des
// joueurs légitimes) — mais sans administrateur, plus personne ne pourrait les
// révoquer. La liste ne dépend jamais de la configuration : le champ
// « parrain » d'une entrée n'est qu'une trace, pas une clé étrangère.
func (p *plugin) révoquer(src command.Source, cible string) error {
	joueur, ok := src.(proxy.Player)
	if !ok {
		return p.refusRévocation(src, uuid.Nil, "console", cible, "la source n'est pas un joueur")
	}
	if !p.cfg.parrains[joueur.ID()] && !p.cfg.admins[joueur.ID()] {
		return p.refusRévocation(src, joueur.ID(), joueur.Username(), cible, "parrain non autorisé")
	}
	// Une cible vide ou démesurée ne peut correspondre à rien d'inscrit ; on
	// la coupe avant d'aller balayer la liste avec.
	if cible == "" || len(cible) > 64 {
		return p.refusRévocation(src, joueur.ID(), joueur.Username(), cible, "nom invalide")
	}

	admin := p.cfg.admins[joueur.ID()]
	e, err := p.liste.retirer(cible, func(e entrée) bool {
		return p.peutRévoquer(joueur.ID(), e)
	})
	switch {
	case errors.Is(err, errAbsent):
		return p.refusRévocation(src, joueur.ID(), joueur.Username(), cible, "personne de ce nom dans la liste")
	case errors.Is(err, errAmbigu):
		return p.refusRévocation(src, joueur.ID(), joueur.Username(), cible,
			"plusieurs entrées portent ce nom, révoque par UUID")
	case errors.Is(err, errPasLeParrain):
		return p.refusRévocation(src, joueur.ID(), joueur.Username(), cible, "ce n'est pas toi qui l'as invité")
	case err != nil:
		p.log.Error(err, "révocation refusée", "auteurUUID", joueur.ID(), "auteur", joueur.Username(),
			"cible", cible, "horodatage", time.Now().UTC(), "résultat", "refusée", "raison", "écriture de la liste")
		return répondre(src, color.Red, "Impossible d'enregistrer la révocation, elle n'est PAS prise en compte.")
	}

	// Une révocation qui laisse la personne en jeu jusqu'à sa prochaine
	// déconnexion n'est pas une révocation. Après le retrait de la liste : si
	// l'expulsion échoue, le joueur ne reviendra de toute façon pas.
	if p.expulser != nil {
		p.expulser(e.UUID, "Ton invitation a été retirée.")
	}

	p.log.Info("révocation", "auteurUUID", joueur.ID(), "auteur", joueur.Username(),
		"retiréUUID", e.UUID, "retiré", e.Pseudo, "parrainDOrigine", e.Parrain,
		"admin", admin, "horodatage", time.Now().UTC(), "résultat", "acceptée")
	return répondre(src, color.Green, e.Pseudo+" ne peut plus se connecter.")
}

// peutRévoquer est la décision d'autorisation de la révocation, isolée pour
// être testable sans joueur connecté. L'UUID nul ne donne aucun droit : une
// entrée orpheline (parrain absent du fichier) n'appartient à personne, seul
// un administrateur la retire.
func (p *plugin) peutRévoquer(auteur uuid.UUID, e entrée) bool {
	if auteur == uuid.Nil {
		return false
	}
	return p.cfg.admins[auteur] || e.Parrain == auteur
}

func (p *plugin) refusRévocation(src command.Source, auteurID uuid.UUID, auteur, cible, raison string) error {
	p.log.Info("révocation refusée", "auteurUUID", auteurID, "auteur", auteur,
		"cible", cible, "horodatage", time.Now().UTC(), "résultat", "refusée", "raison", raison)
	return répondre(src, color.Red, "Révocation refusée : "+raison+".")
}

func (p *plugin) refuser(src command.Source, parrainID uuid.UUID, parrain, pseudo, raison string) error {
	p.log.Info("invitation refusée", "parrainUUID", parrainID, "parrain", parrain,
		"invité", pseudo, "horodatage", time.Now().UTC(), "résultat", "refusée", "raison", raison)
	return répondre(src, color.Red, "Invitation refusée : "+raison+".")
}

// mojangProfilURL est une variable pour que le test pointe sur un serveur
// httptest : aucun appel réseau en test.
var mojangProfilURL = "https://api.mojang.com/users/profiles/minecraft/"

var errPseudoInconnu = errors.New("pseudo inconnu de Mojang")

// résoudreMojang traduit un pseudo en UUID auprès de l'API Mojang. Gate est le
// seul conteneur avec un accès sortant, c'est donc lui qui peut le faire.
//
// Échec fermé : tout ce qui n'est pas un 200 avec un UUID exploitable est une
// erreur. 404 et 204 (réponse vide, ancienne forme de l'API) veulent dire
// « ce pseudo n'existe pas » ; le reste est un incident.
func résoudreMojang(ctx context.Context, pseudo string) (uuid.UUID, string, error) {
	ctx, annuler := context.WithTimeout(ctx, 5*time.Second)
	defer annuler()

	// pseudo a déjà passé pseudoValide : [A-Za-z0-9_]{3,16}, rien à échapper.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, mojangProfilURL+pseudo, nil)
	if err != nil {
		return uuid.Nil, "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return uuid.Nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNoContent, http.StatusNotFound:
		return uuid.Nil, "", errPseudoInconnu
	default:
		return uuid.Nil, "", fmt.Errorf("API Mojang : statut %d", resp.StatusCode)
	}

	var profil struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	// Corps borné : la réponse fait ~100 octets. On ne se laisse pas noyer par
	// un intermédiaire qui répondrait un flux infini.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&profil); err != nil {
		return uuid.Nil, "", fmt.Errorf("réponse Mojang illisible : %w", err)
	}
	// L'API renvoie l'UUID sans tirets ; uuid.Parse accepte les deux formes.
	id, err := uuid.Parse(profil.ID)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, "", fmt.Errorf("UUID Mojang invalide %q", profil.ID)
	}
	if !pseudoValide.MatchString(profil.Name) {
		// Le pseudo canonique revient de l'extérieur : il repasse la frontière
		// de confiance avant d'être écrit dans la liste et affiché en jeu.
		return uuid.Nil, "", fmt.Errorf("pseudo canonique inattendu de Mojang")
	}
	return id, profil.Name, nil
}

func répondre(src command.Source, c color.Color, texte string) error {
	return src.SendMessage(&component.Text{Content: texte, S: component.Style{Color: c}})
}
