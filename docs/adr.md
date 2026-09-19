# Décisions d'architecture

Format court : décision, pourquoi, ce qui a été écarté.

## 0001 — Fabric, pas Forge ni Paper, pour le serveur Minecraft
**Décidé :** Fabric + Lithium, FerriteCore, Krypton (mods serveur-only).
**Pourquoi :** l'objectif est une expérience *vanilla*. Fabric ne change aucun
comportement de jeu ; Lithium seul coupe 30-50 % du temps de tick. Paper réécrit
volontairement la redstone et le comportement des mobs pour la performance — ça
casse les fermes et la parité technique. Forge est un framework de mods de
contenu : plus d'internes modifiés, démarrage plus lent, plus de RAM, pour un
bénéfice nul quand on n'installe aucun mod de contenu.
**Écarté :** Vanilla pur (aucun mod de perf, et Gate exige le forwarding moderne
que seul FabricProxy-Lite fournit), Paper, Forge/NeoForge.

## 0002 — Gate comme proxy, config en YAML, pas en base de données
**Décidé :** Gate (Go, ~10 Mo de RAM) en mode complet avec forwarding moderne.
La config reste un fichier YAML versionné.
**Pourquoi :** Gate surveille déjà son fichier et recharge à chaud les `servers`,
le MOTD et le mode Lite, sans redémarrage ni déconnexion. Une base de données
n'accélère rien — le YAML est parsé une fois et vit en mémoire ; un aller-retour
Postgres serait strictement plus lent. Elle ajoute une dépendance qui peut tomber
sur le chemin critique de la connexion des joueurs.
**Écarté :** Velocity (JVM, ~10× la RAM pour le même travail), BungeeCord
(forwarding non sécurisé), config en base.
**Ce qui justifie quand même du Go maison :** Gate s'importe comme librairie
(`go.minekube.com/gate/pkg/gate`). Le binaire maison sert à la fenêtre de
maintenance (ADR 0004) — pas à relire de la config.

## 0003 — Secrets : SOPS + age, pas de gestionnaire de secrets hébergé
**Décidé :** SOPS avec age, fichiers chiffrés commités dans le dépôt public.
**Pourquoi :** SOPS chiffre valeur par valeur — la structure reste lisible, les
diffs restent propres, pas de conflit de merge sur un blob. Zéro service à
opérer, zéro dépendance réseau à l'exécution. Les secrets atterrissent en
`podman secret` sur l'hôte, montés en `/run/secrets` (tmpfs), jamais en variable
d'environnement (visible dans `podman inspect`).
**Écarté :** ansible-vault (chiffre le fichier entier : diffs inutilisables),
OpenBao / Vault / Infisical (un service de plus à héberger, sauvegarder et
déverrouiller — pour un opérateur et un serveur, le coût dépasse le gain).

## 0004 — Avertissements de maintenance : plugin Gate + RCON
**Décidé :** un binaire Gate maison qui, sur signal, diffuse un compte à rebours
(15 / 5 / 1 min), bascule le MOTD en mode maintenance, puis laisse le conteneur
Minecraft redémarrer pendant que Gate garde la connexion.
**Pourquoi :** c'est le proxy qui survit au redémarrage du backend — il est le
seul endroit d'où on peut parler aux joueurs *pendant* l'indisponibilité.
**Écarté :** `podman-auto-update` seul sur le conteneur Minecraft (il redémarre
sans prévenir personne), reboot automatique de `dnf-automatic` (même problème,
en pire).

## 0005 — Services OCI gratuits : oui, mais jamais sur le chemin critique
**Décidé :** on utilise les services Always Free d'Oracle là où ils résolvent un
problème qu'on ne peut pas résoudre sur la machine elle-même, et seulement si
les remplacer prend moins d'une heure.

Retenus :
- **Object Storage** (~20 Go gratuits, API compatible S3) — *deuxième* dépôt
  restic. Restauration rapide et gratuite en egress quand le problème est le
  serveur, pas Oracle.
- **Email Delivery** — le SMTP sortant depuis une IP de cloud public est bloqué
  à peu près partout ; Forgejo et le serveur d'authentification ont besoin
  d'envoyer des courriels. C'est le service qui rapporte le plus pour l'effort.
- **Bastion** — accès SSH sans port 22 ouvert sur Internet.
- **Functions + Notifications** — sonde externe de disponibilité. Une machine ne
  peut pas surveiller sa propre panne ; c'est le seul rôle qui *exige* d'être
  ailleurs.

Écartés :
- **Load Balancer** (10 Mbps gratuits) — Caddy sur la VM x86 fait le même travail
  sans plafond de débit.
- **Monitoring / Logging OCI** — Grafana + Loki en local, parce que l'observabilité
  doit survivre au départ d'Oracle et qu'elle fait partie de ce qu'on démontre.
- **Autonomous Database** — c'est de l'Oracle DB ; les services visés parlent
  Postgres.

**Contrainte qui tient :** aucun de ces services n'est sur le chemin de connexion
d'un joueur, et le dépôt restic hors Oracle (ADR 0006) reste la référence.

## 0006 — Sauvegardes : deux dépôts, dont un hors Oracle
**Décidé :** restic écrit dans deux dépôts — OCI Object Storage (rapide, gratuit)
et un fournisseur tiers à egress gratuit (Cloudflare R2 ou Backblaze B2).
**Pourquoi :** une sauvegarde chez le fournisseur qui vient de vous couper ne
sert à rien. Le coût du second dépôt est de quelques dollars par mois au plus
pour ce volume — c'est l'assurance contre le scénario qui a motivé le projet.
**À vérifier :** la restauration, pas la sauvegarde. Un exercice trimestriel,
noté dans `docs/runbook-restore.md`.
