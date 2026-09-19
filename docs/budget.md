# Budget annuel

Plafond fixé : 200 $ CA par année. Conclusion : le plan recommandé en utilise
la moitié, et le reste ne devrait pas être dépensé.

## Recommandé — à l'année, ~100 $ CA

| Poste | Fournisseur | Coût annuel |
|---|---|---|
| VPS 8 Go, 4 vCore, Montréal | Elixior ou équivalent, ~6,50 $/mois | ~78 $ |
| Nom de domaine | n'importe quel registraire | ~20 $ |
| Sauvegarde hors site, ~20 Go | Backblaze B2 (0,006 $ US/Go/mois) | ~2 $ |
| **Total** | | **~100 $** |

## Variante saisonnière — ~60 $ CA

Le VPS facturé au mois, détruit hors saison et reconstruit au retour du groupe
(voir `hebergement.md`). Domaine et sauvegardes restent actifs à l'année : ce
sont eux qui rendent la reconstruction possible.

| Poste | Coût annuel |
|---|---|
| VPS, 5 mois actifs | ~33 $ |
| Domaine | ~20 $ |
| Sauvegarde B2 | ~2 $ |
| **Total** | **~55 $** |

## Ce qu'il ne faut PAS faire avec les 100 $ restants

**Acheter plus de RAM.** 8 Go couvrent le jeu plus Forgejo et Grafana. Au-delà,
elle dort.

**Prendre un serveur dédié d'entrée de gamme** (Kimsufi et équivalents, à partir
d'environ 11 $/mois). C'est contre-intuitif mais c'est un piège pour cette
charge : ces machines sont du matériel ancien à basse fréquence. La boucle de
tick de Minecraft est mono-thread — un vieux Xeon dédié à 2,1 GHz donne un moins
bon TPS qu'un vCPU partagé moderne. « Dédié » protège du voisin bruyant, pas de
la lenteur.

**Prépayer à l'année pour la remise.** Elle tourne autour de 20 %, soit une
quinzaine de dollars, en échange de la perte de l'option saisonnière et d'une
exposition à la disparition d'un petit fournisseur. Mauvais échange.

## Où l'argent vaudrait la peine, si jamais

Par ordre de rendement réel :
1. **Un VPS à meilleure fréquence monocœur**, mesuré au `sysbench`, pas deviné.
   C'est le seul achat qui améliore directement le TPS.
2. **Un second dépôt de sauvegarde** chez un autre fournisseur (~2 $/an).
   Rendement énorme par dollar.
3. Rien d'autre, tant qu'une mesure ne le justifie pas.
