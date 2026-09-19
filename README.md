# caffelatte

Infrastructure d'un petit serveur communautaire, décrite entièrement en code.
Minecraft (Java + Bedrock), Git hébergé, authentification, journalisation.
Tout en conteneurs rootless, déployé par Ansible, sauvegardé hors site.

Le dépôt est public. Aucune adresse, aucun nom d'hôte, aucun secret en clair
n'y figure — l'inventaire réel est hors dépôt, les secrets sont chiffrés (SOPS).

## Objectif

Apprendre Oracle Cloud Infrastructure — compartiments, politiques IAM, VCN et
Security Lists, Bastion, budgets, stockage objet — sur une charge réelle plutôt
que sur un tutoriel. Le serveur Minecraft est le prétexte : il faut que quelque
chose tourne pour que les décisions d'infrastructure aient des conséquences.

Objectif secondaire : à 0 $. Voir `docs/couts.md`.

Ce n'est **pas** la façon la moins chère d'héberger un serveur Minecraft. Un VPS
à Montréal coûterait quelques dollars par mois avec plus de RAM garantie et
aucun jeu de capacité. Mais un VPS n'enseigne aucun concept infonuagique.

## Contrainte de conception

Le tier gratuit d'Oracle Cloud est l'hébergement actuel, pas une dépendance.
Oracle a réduit l'allocation ARM gratuite de 4 OCPU / 24 Go à **2 OCPU / 12 Go**
au début de juin 2026, sans annonce ni notification. Le projet part donc du principe
que l'hébergeur peut disparaître du jour au lendemain :

- rien de spécifique à OCI dans le code (pas de service managé, pas de SDK) ;
- l'état complet tient dans des volumes Podman, sauvegardés chiffrés chez un
  fournisseur objet tiers ;
- une reconstruction est `ansible-playbook site.yml` contre n'importe quel hôte
  Enterprise Linux, puis `restic restore`.

Le test qui compte n'est pas « la sauvegarde tourne » mais « je reconstruis
ailleurs en moins d'une heure ». Voir `docs/runbook-restore.md`.

## Architecture

```
                    Bedrock (UDP 19132)     Java (TCP 25565)
                            │                      │
                         Geyser ──────────────► Gate ◄──── binaire Go maison
                                                   │        (fenêtre de maintenance)
                                            réseau interne `mc`
                                                   │
                                            Minecraft (Fabric)
                                            aucun port sur l'hôte
```

| Rôle | Choix | Pourquoi (détail dans `docs/adr.md`) |
|---|---|---|
| Serveur Minecraft | Fabric + Lithium/FerriteCore/Krypton | parité vanilla exacte ; Paper casse la redstone, Forge est pour les mods de contenu |
| Proxy | Gate (Go) | ~10 Mo de RAM, hot-reload natif, s'embarque comme librairie |
| Bedrock | Geyser standalone + Floodgate | garde le serveur de jeu pur |
| Conteneurs | Podman rootless + Quadlet | unités systemd natives, pas de démon, pas de root |
| Secrets | SOPS + age | chiffrement par valeur, diffs lisibles, zéro service à opérer |
| Mises à jour | `dnf-automatic` + `podman auto-update` | natif ; le redémarrage passe par la fenêtre de maintenance |
| Sauvegarde | restic → S3 tiers | dédupliqué, chiffré, vérifié à chaque passe |

## État

- [x] Socle Ansible, Podman rootless, mises à jour automatiques
- [x] Image Minecraft maison (Fabric, construite depuis zéro)
- [x] Sauvegarde restic hors site avec gel du monde
- [ ] Binaire Gate maison + plugin de maintenance
- [ ] Geyser / Floodgate
- [x] Budget et alertes (Terraform) — pas de bastion, voir ADR 0010

Services envisagés, mis en attente : Forgejo, OIDC, Grafana + Loki, chat vocal.
Aucun n'enseigne quoi que ce soit sur OCI, et chacun consomme une part des 12 Go.
À ajouter un par un une fois le socle stable — pas avant.

## Hors périmètre, et pourquoi

**Mastodon** — environ 3 Go de RAM pour Puma, Sidekiq, Postgres et Redis. Sur
12 Go partagés avec un serveur de jeu, il évince des services qui servent
vraiment. À reconsidérer si la machine grossit.

**Bunny CDN** — un CDN sert à absorber de l'egress statique. Le tier Oracle en
donne 10 To par mois et il n'y a pas de média volumineux à servir. À ajouter le
jour où il y a une mesure qui le justifie, pas avant.

**Base de données pour la config de Gate** — Gate recharge déjà son YAML à chaud
sans déconnecter personne. Une base ne rendrait rien plus rapide et mettrait une
dépendance réseau sur le chemin de connexion des joueurs.
