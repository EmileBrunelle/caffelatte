package maintenance

import "time"

// Window décrit un redémarrage planifié du backend.
type Window struct {
	StartsAt time.Time
	Reason   string // « mise à jour du serveur », « redémarrage de l'OS »
}

// Warnings est le compte à rebours diffusé avant StartsAt.
var Warnings = []time.Duration{15 * time.Minute, 5 * time.Minute, time.Minute}

// Register branche le plugin sur le proxy.
func Register() {
	// TODO(émile) : voir le README de la section « À toi de jouer ».
	//
	// Ce que le plugin doit faire, dans l'ordre :
	//   1. à chaque palier de Warnings, diffuser un message aux joueurs connectés
	//   2. à StartsAt, remplacer le MOTD par l'état de maintenance
	//   3. pendant l'indisponibilité du backend, décider du sort des joueurs
	//   4. à la réapparition du backend, les y ramener
	//
	// L'étape 3 est la décision de conception : voir handleDuringOutage.
	panic("non implémenté")
}

// handleDuringOutage décide de ce qui arrive à un joueur qui est connecté
// (ou qui se connecte) pendant que le backend Minecraft est absent.
//
// TODO(émile) : implémenter. Trois stratégies tenables, aucune gratuite :
//
//   - Déconnexion propre, avec un message qui donne l'heure de retour.
//     Simple, honnête, mais le joueur doit se souvenir de revenir.
//   - Salle d'attente : Gate garde la connexion TCP ouverte et rebranche le
//     joueur dès que le backend répond. Meilleure expérience, mais un joueur
//     Minecraft déconnecté du monde sans écran voit un client figé — il faut
//     un backend « limbo » minimal pour lui montrer quelque chose.
//   - File d'attente ordonnée, qui rebranche dans l'ordre d'arrivée. Évite la
//     ruée de reconnexions qui fait ramer le serveur au redémarrage — ce qui
//     compte vraiment avec 2 OCPU.
//
// Le vrai arbitrage : combien de complexité vaut le fait qu'un joueur ne
// remarque pas le redémarrage ? Ça dépend de la taille de la communauté et de
// la fréquence des mises à jour, et c'est toi qui connais ça.
func handleDuringOutage() { panic("non implémenté") }
