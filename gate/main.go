// Binaire Gate maison : Gate standard, plus un plugin de fenêtre de maintenance.
//
// Raison d'être : quand le conteneur Minecraft redémarre (nouvelle image, reboot
// de l'OS), c'est le proxy qui survit. Il est le seul endroit d'où on peut
// prévenir les joueurs, puis les retenir pendant l'indisponibilité au lieu de
// les éjecter avec « Connection refused ».
package main

import (
	"go.minekube.com/gate/cmd/gate"

	"github.com/OWNER/caffelatte/gate/internal/maintenance"
)

func main() {
	maintenance.Register()
	gate.Execute()
}
