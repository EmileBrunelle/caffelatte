# Budget annuel

Plafond fixé : 200 $ CA par année. Conclusion : le plan recommandé en utilise
la moitié, et le reste ne devrait pas être dépensé.

## Recommandé — Oracle d'abord, VPS en assurance : ~22 $ CA par année

Contrainte retenue : le serveur tourne **à l'année**. C'est ce qui rend Oracle
viable — un serveur permanent avec `-Xms4G` sur 12 Go garde 33 % de mémoire
réservée en continu, donc le critère de récupération pour inactivité (CPU,
réseau ET mémoire tous sous 20 % sur 7 jours) n'est jamais rempli.

| Poste | Coût annuel |
|---|---|
| VM ARM Oracle Always Free, 2 OCPU / 12 Go | 0 $ |
| Nom de domaine | ~20 $ |
| Sauvegarde hors site B2, ~20 Go | ~2 $ |
| **Total** | **~22 $** |

Le reste du budget (~175 $) n'est pas dépensé : c'est le **fonds de migration**.
Si Oracle récupère l'instance ou change ses conditions, le VPS de Montréal est
en service le jour même avec le même playbook. Une année de VPS 4 Go coûte
150-330 $ — le fonds en couvre une, tout juste.

Exploiter le gratuit d'Oracle vaut la peine **précisément parce que le coût de
sortie est d'une heure**. Sans la portabilité construite dans `ansible/`, ce
serait un pari ; avec elle, c'est de l'argent gratuit.

### La règle d'arrêt

La capacité ARM à Montréal est intermittente. Lancer la boucle de réessai de
`runbook-capacite-arm.md` et se donner **trois semaines**. Passé ce délai,
acheter le VPS et ne plus y penser — le temps passé à surveiller une loterie
dépasse vite les 78 $ qu'elle économise.

## Repli — et c'est un compromis, pas un équivalent

Plafond de 200 $ CA/an = environ 12 $ US/mois. Chez un fournisseur établi de
Montréal, ça achète **2 à 3 Go**, pas 8. Un tas Java de 4 Go n'y entre pas.

Le repli réaliste est donc OVHcloud Beauharnois, entrée autour de 4 Go, avec un
tas réduit à 3 Go, `view-distance` et `simulation-distance` baissés, et aucun
service supplémentaire à côté du jeu. Ça marche pour 5-10 joueurs en vanilla,
mais c'est une version diminuée.

Conséquence sur la règle d'arrêt : si Oracle ne donne rien après trois semaines,
le choix n'est plus « acheter l'équivalent », c'est « accepter moins, ou
continuer d'attendre ». À décider à ce moment-là, pas maintenant.

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
