# Choisir l'hôte

Rien dans `ansible/` ne dépend du fournisseur. Seul `terraform/` est captif
d'OCI. Changer d'hôte = relancer le playbook ailleurs et restaurer restic.
Le choix est donc réversible, et ne doit pas bloquer la construction.

## Le seul chiffre qui compte pour Minecraft

**La performance monocœur.** La boucle de tick de Minecraft est
mono-thread : elle ne s'étale pas sur plusieurs cœurs. Un VPS à 8 vCPU lents
donne un moins bon TPS qu'un VPS à 2 vCPU rapides. La RAM et le nombre de cœurs
sont secondaires une fois 4 Go atteints pour 5-10 joueurs.

C'est exactement le piège des offres à bas prix : elles optimisent le go de RAM
et le téraoctet de transfert, qui sont ce qui se vend, pas la fréquence par cœur.

## Filtre avant d'acheter

1. **KVM obligatoire.** OpenVZ et LXC ne donnent pas les cgroups v2 ni les
   espaces de noms utilisateur dont Podman rootless a besoin. Toute la pile de
   ce dépôt échoue sur un conteneur revendu.
2. **Pas un « storage VPS ».** Ces offres (le cœur de métier de Servarica, par
   exemple) donnent énormément de disque pour presque rien, avec du CPU
   partagé et sursouscrit. Excellent pour des sauvegardes, mauvais pour un tick
   de jeu.
3. **Mesurer avant de s'engager.** Demander une IP de test, ou prendre au mois,
   et exécuter : `sysbench cpu --threads=1 run`. Comparer entre deux offres
   plutôt que de lire les descriptions.
4. **Vérifier l'emplacement réel.** « Canada » veut parfois dire une revente
   dans un centre de données qu'on ne contrôle pas. Beauharnois, Montréal et
   Toronto se vérifient par un traceroute.

## Dimensionner : tu n'as pas besoin de 12 Go

12 Go est l'allocation d'Oracle, pas un besoin. Pour 5-10 joueurs en vanilla
Fabric : 4 Go de tas plus environ 1 Go pour le système et le proxy. Un VPS de
**6 à 8 Go** est confortable et laisse de la place pour ajouter Forgejo ou
Grafana plus tard.

Prendre 12 Go parce qu'Oracle en donnait 12 serait s'ancrer sur le mauvais
chiffre — surtout que le goulot est le CPU monocœur, pas la RAM.

## Prix réels au Québec (vérifiés 2026-09-19)

Le 6,50 $ CA pour 8 Go vu sur LowEndBox venait d'Elixior. **Leur site ne répond
plus** (certificat auto-signé). Ce n'était pas un prix de marché : c'était un
nouvel entrant qui achetait des clients, et il a disparu en moins d'un an.
Leçon retenue — ce point de prix n'existe pas de façon durable.

Prix chez un fournisseur établi (ExtraVM, Montréal, en affaires depuis 2014,
Ryzen 7 + NVMe + protection DDoS, en **dollars US**, facturation mensuelle) :

| RAM | Cœurs | Prix/mois US | ≈ $ CA/an |
|---|---|---|---|
| 2 Go | 2 | 10 $ | ~165 $ |
| 4 Go | 2 | 20 $ | ~330 $ |
| 8 Go | 4 | 40 $ | ~660 $ |
| 12 Go | 4 | 60 $ | ~990 $ |

ServaRica (Montréal, 14 ans, matériel en propre, pas de sursouscription) et
OVHcloud Beauharnois sont les autres options établies ; OVH est le moins cher
du lot et le repli réaliste sous contrainte de budget — à vérifier au moment
d'acheter, et à passer au `sysbench` monocœur avant de s'engager.

**Ce que ça révèle :** les 12 Go d'Oracle valent autour de 990 $ US par année au
prix d'un VPS de jeu équivalent. Le tier gratuit n'est plus « l'option pas chère
mais risquée » — c'est la seule qui atteint la cible dans le budget.

## Ne pas payer 12 mois pour 4 mois de jeu

Un serveur entre amis se joue par vagues : un mois intense quand une version
sort, puis plus rien pendant l'été. Payer 12 mois pour ça est le vrai gaspillage,
pas le prix mensuel.

La facturation au mois permet de **détruire le VPS et de le recréer** quand le
groupe revient. C'est exactement la capacité construite pour le scénario
« Oracle disparaît » — `ansible-playbook site.yml` contre un hôte neuf, puis
`restic restore`. Le monde, les bans et les ops sont dans les sauvegardes ; le
serveur lui-même n'a aucun état à préserver.

    # Fin de saison
    /home/svc/bin/backup.sh && <détruire le VPS>

    # Retour, sur un hôte neuf, environ une heure
    ansible-playbook site.yml
    restic restore latest --target /

Coût réel à 6,50 $/mois pour 4-5 mois actifs : **30 à 35 $ par année**.

C'est le paiement du travail d'infrastructure : une installation reproductible
n'est pas qu'une bonne pratique à montrer, c'est ce qui rend l'hébergement
jetable — et donc bon marché.

À faire pour que ça tienne : un exercice de restauration pendant que le serveur
tourne encore. Une restauration jamais testée n'est pas une restauration, et on
la découvre au pire moment.

## État de la décision

- **Oracle `ca-montreal-1`, Always Free** — 0 $, mais la capacité ARM est une
  loterie et la région n'a qu'un domaine de disponibilité. Bon pour apprendre
  OCI, mauvais pour « quelque chose de solide ».
- **OVHcloud Beauharnois** — Québec, entrée autour de 6 à 14 $ CA par mois
  selon la configuration (à vérifier, les prix bougent). API et provider
  Terraform réels, donc l'apprentissage infonuagique reste possible.
- **Offres LowEndBox canadiennes** — le meilleur rapport dollar/ressource, à
  condition d'appliquer le filtre ci-dessus. Le prix affiché ne dit rien du TPS.

Garder un compte Oracle Always Free en parallèle ne coûte rien et reste le
terrain d'apprentissage IAM, VCN et Bastion, même si le jeu tourne ailleurs.
