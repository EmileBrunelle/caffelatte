package invitation

import (
	"sort"
	"strings"

	"go.minekube.com/gate/pkg/util/uuid"
)

// arbreInvitations construit l'affichage indenté « qui a invité qui ».
// Fonction pure sur une copie de la liste, sans dépendance à Gate : c'est ce
// qui la rend testable sans proxy.
//
// Deux pièges traités :
//
//   - Les orphelins. Le champ Parrain n'est qu'une trace, pas une clé
//     étrangère (voir invitation.go:350-359) : retirer un parrain n'invalide
//     pas ses invités. Toute entrée dont le Parrain n'est pas dans la liste
//     — invitée directement par un parrain de la configuration, ou par
//     quelqu'un qui n'y est plus — est donc une racine légitime, jamais
//     perdue. On les regroupe sous un même intitulé plutôt que de les faire
//     disparaître d'un parcours qui ne descendrait que depuis des parrains
//     connus.
//   - Les cycles. Impossibles en usage normal (le parrain est déjà dans la
//     liste au moment de l'invitation), mais le JSON est modifiable à la
//     main. Un ensemble de visités coupe court à toute boucle, y compris une
//     entrée qui est son propre parrain ; ce qui reste non visité après le
//     parcours normal (donc jamais atteignable depuis une racine) est
//     affiché à part plutôt que d'être avalé en silence.
//
// Invariant maintenu par construction : chaque entrée de autorisés est
// écrite exactement une fois, soit dans l'arbre, soit dans la section des
// cycles — jamais les deux, jamais aucune.
func arbreInvitations(autorisés map[uuid.UUID]entrée) string {
	if len(autorisés) == 0 {
		return "Personne n'est invité pour l'instant."
	}

	enfants := map[uuid.UUID][]entrée{}
	var racines []entrée
	for _, e := range autorisés {
		if _, parrainDansLaListe := autorisés[e.Parrain]; parrainDansLaListe {
			enfants[e.Parrain] = append(enfants[e.Parrain], e)
		} else {
			racines = append(racines, e)
		}
	}
	for _, l := range enfants {
		trierEntrées(l)
	}
	trierEntrées(racines)

	var b strings.Builder
	visitées := map[uuid.UUID]bool{}
	if len(racines) > 0 {
		b.WriteString("Parrain absent de la liste (invités directement, ou par quelqu'un qui n'y est plus) :\n")
		for _, r := range racines {
			écrireBranche(&b, r, enfants, visitées, 1)
		}
	}

	// Tout ce qui n'a pas été visité n'est atteignable depuis aucune racine :
	// un cycle pur (chaque membre a un parrain présent dans la liste) ou une
	// entrée qui est son propre parrain. Sans ce filet, le total afficherait
	// moins que len(autorisés) et l'arbre mentirait sur qui a accès.
	var cycliques []entrée
	for _, e := range autorisés {
		if !visitées[e.UUID] {
			cycliques = append(cycliques, e)
		}
	}
	if len(cycliques) > 0 {
		trierEntrées(cycliques)
		b.WriteString("Parrainage circulaire détecté (fichier corrompu) :\n")
		for _, e := range cycliques {
			visitées[e.UUID] = true
			b.WriteString("  " + étiquette(e) + "\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

// écrireBranche écrit une entrée puis descend récursivement dans ses invités.
// Le garde-fou « déjà visitée » est ce qui empêche un cycle édité à la main
// de boucler à l'infini sur une commande déclenchable en jeu.
func écrireBranche(b *strings.Builder, e entrée, enfants map[uuid.UUID][]entrée, visitées map[uuid.UUID]bool, profondeur int) {
	if visitées[e.UUID] {
		return
	}
	visitées[e.UUID] = true
	b.WriteString(strings.Repeat("  ", profondeur) + étiquette(e) + "\n")
	for _, enfant := range enfants[e.UUID] {
		écrireBranche(b, enfant, enfants, visitées, profondeur+1)
	}
}

// étiquette affiche le pseudo, ou l'UUID si le pseudo est vide.
func étiquette(e entrée) string {
	nom := e.Pseudo
	if nom == "" {
		nom = e.UUID.String()
	}
	return nom + " (" + e.Date.Format("2006-01-02") + ")"
}

// trierEntrées trie par date puis pseudo : une map Go itère dans un ordre
// aléatoire, sans ce tri deux appels sur la même liste donneraient deux
// sorties différentes.
func trierEntrées(es []entrée) {
	sort.Slice(es, func(i, j int) bool {
		if !es[i].Date.Equal(es[j].Date) {
			return es[i].Date.Before(es[j].Date)
		}
		return es[i].Pseudo < es[j].Pseudo
	})
}
