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
