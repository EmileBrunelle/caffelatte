package invitation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.minekube.com/gate/pkg/util/uuid"
)

// entrée est un joueur autorisé. L'UUID est la clé : les pseudos changent, pas
// les UUID. Le pseudo n'est là que pour la lisibilité du fichier et des
// journaux — aucune décision ne se prend dessus.
type entrée struct {
	UUID    uuid.UUID `json:"uuid"`
	Pseudo  string    `json:"pseudo"`
	Parrain uuid.UUID `json:"parrain"`
	Date    time.Time `json:"date"`
}

// fichier est la forme sur disque. Le plafond par parrain n'a pas de compteur
// dédié : il se déduit du nombre d'entrées portant son UUID. Un compteur
// séparé pourrait diverger de la liste, pas ce décompte.
type fichier struct {
	Autorises []entrée `json:"autorises"`
}

var (
	errDéjàAutorisé = errors.New("ce joueur est déjà autorisé")
	errPlafond      = errors.New("plafond d'invitations atteint")
	errAbsent       = errors.New("ce joueur n'est pas dans la liste")
	errAmbigu       = errors.New("plusieurs entrées portent ce nom")
	errPasLeParrain = errors.New("ce n'est pas toi qui l'as invité")
)

// liste est la liste d'autorisation de Gate, en mémoire et sur disque.
type liste struct {
	chemin string

	mu        sync.Mutex
	autorisés map[uuid.UUID]entrée
}

// chargerListe lit la liste depuis le disque.
//
// Fichier absent : liste vide, c'est le premier démarrage. Fichier illisible
// ou JSON invalide : ERREUR, jamais une liste vide silencieuse — repartir de
// zéro sur un fichier corrompu éjecterait tous les joueurs légitimes sans que
// personne ne comprenne pourquoi, et l'opérateur doit voir le problème.
func chargerListe(chemin string) (*liste, error) {
	l := &liste{chemin: chemin, autorisés: map[uuid.UUID]entrée{}}

	b, err := os.ReadFile(chemin)
	if errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lecture de %q : %w", chemin, err)
	}

	var f fichier
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%q est illisible (JSON invalide) : %w", chemin, err)
	}
	for _, e := range f.Autorises {
		if e.UUID == uuid.Nil {
			return nil, fmt.Errorf("%q contient une entrée sans UUID", chemin)
		}
		l.autorisés[e.UUID] = e
	}
	return l, nil
}

// contient dit si l'UUID est autorisé à se connecter.
func (l *liste) contient(id uuid.UUID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.autorisés[id]
	return ok
}

// ajouter inscrit un invité, sous plafond, et persiste immédiatement.
//
// Vérification du plafond, ajout et écriture se font sous le même verrou :
// deux parrains qui invitent en même temps ne peuvent ni dépasser le plafond
// ni s'écraser mutuellement sur le disque. Si l'écriture échoue, l'ajout en
// mémoire est annulé — la liste en RAM ne promet jamais plus que le disque.
func (l *liste) ajouter(e entrée, plafond int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if ancienne, ok := l.autorisés[e.UUID]; ok {
		// Déjà autorisé. Si le joueur a changé de pseudo, on rafraîchit
		// l'étiquette sans consommer de place : l'UUID est inchangé.
		if ancienne.Pseudo == e.Pseudo {
			return errDéjàAutorisé
		}
		ancienne.Pseudo = e.Pseudo
		l.autorisés[e.UUID] = ancienne
		if err := l.enregistrer(); err != nil {
			return err
		}
		return errDéjàAutorisé
	}

	// ponytail : décompte linéaire à chaque invitation. La liste tient dans
	// quelques dizaines d'entrées ; indexer par parrain le jour où elle en
	// compte des milliers.
	n := 0
	for _, a := range l.autorisés {
		if a.Parrain == e.Parrain {
			n++
		}
	}
	if n >= plafond {
		return errPlafond
	}

	l.autorisés[e.UUID] = e
	if err := l.enregistrer(); err != nil {
		delete(l.autorisés, e.UUID)
		return err
	}
	return nil
}

// retirer supprime une entrée et persiste immédiatement.
//
// La cible est soit un UUID, soit le pseudo Java ou le gamertag Bedrock
// exactement tel qu'il est inscrit (casse ignorée) : une seule liste, donc un
// seul chemin de révocation pour les deux types d'entrée. Deux entrées
// homonymes (un pseudo Java et un gamertag Xbox identiques, par exemple)
// donnent errAmbigu plutôt qu'une suppression au hasard — on révoque alors par
// UUID, que le fichier affiche.
//
// Recherche, autorisation et suppression sous le MÊME verrou : deux
// révocations concurrentes ne peuvent pas se marcher dessus, et personne ne
// peut être retiré sur la foi d'une autorisation calculée avant que
// quelqu'un d'autre ne modifie l'entrée. Si l'écriture échoue, l'entrée est
// remise — la liste en RAM ne refuse jamais plus que le disque.
func (l *liste) retirer(cible string, autorisé func(entrée) bool) (entrée, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var trouvée entrée
	n := 0
	if id, err := uuid.Parse(cible); err == nil {
		if e, ok := l.autorisés[id]; ok {
			trouvée, n = e, 1
		}
	} else {
		// ponytail : balayage linéaire. Quelques dizaines d'entrées ;
		// indexer par nom le jour où elles se comptent en milliers.
		for _, e := range l.autorisés {
			if strings.EqualFold(e.Pseudo, cible) {
				trouvée, n = e, n+1
			}
		}
	}
	switch {
	case n == 0:
		return entrée{}, errAbsent
	case n > 1:
		return entrée{}, errAmbigu
	}
	if !autorisé(trouvée) {
		return trouvée, errPasLeParrain
	}

	delete(l.autorisés, trouvée.UUID)
	if err := l.enregistrer(); err != nil {
		l.autorisés[trouvée.UUID] = trouvée
		return trouvée, err
	}
	return trouvée, nil
}

// enregistrer écrit la liste de façon atomique. À appeler sous verrou.
//
// Fichier temporaire dans le MÊME répertoire (rename n'est atomique que sur un
// même système de fichiers), fsync avant le rename : une coupure de courant
// laisse soit l'ancien fichier, soit le nouveau, jamais un JSON tronqué.
func (l *liste) enregistrer() error {
	f := fichier{Autorises: make([]entrée, 0, len(l.autorisés))}
	for _, e := range l.autorisés {
		f.Autorises = append(f.Autorises, e)
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(l.chemin), ".invitations-*.json")
	if err != nil {
		return fmt.Errorf("fichier temporaire : %w", err)
	}
	défait := true
	defer func() {
		if défait {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	if _, err := tmp.Write(b); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), l.chemin); err != nil {
		return fmt.Errorf("rename vers %q : %w", l.chemin, err)
	}
	défait = false
	return nil
}
