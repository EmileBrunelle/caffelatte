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

## 0007 — Terraform pour le provisionnement OCI, Ansible pour la configuration
**Décidé :** Terraform ne crée que les ressources qui ont une API chez Oracle —
VCN, Security Lists, instances, bucket, IAM. Ansible configure tout ce qui tourne
à l'intérieur. La frontière est nette et rien ne la traverse.
**Pourquoi :** c'est la Security List qui justifie l'outil, pas l'instance. Une
règle réseau posée à la main dans la console ne se relit pas, ne se révise pas,
et se perd. C'est aussi la couche où une erreur ouvre un port sur Internet.
**La nuance qui compte :** la couche Terraform est la partie *jetable* du projet.
Elle est spécifique à OCI ; si Oracle disparaît, elle est à réécrire. La couche
Ansible, elle, est portable vers n'importe quel hôte Enterprise Linux. C'est
pourquoi la frontière est là et pas ailleurs — on isole ce qui est prisonnier
du fournisseur.
**Écarté :** tout en Ansible via le module `oracle.oci` (mélange création et
configuration, pas de plan, pas de suppression fiable) ; console à la main (une
vingtaine de minutes, mais irreproductible — or « reconstruire ailleurs » est
l'exigence qui a lancé le projet) ; Terraform aussi pour les conteneurs (le
provider Podman existe et ne vaut rien face à Quadlet).

## 0008 — Un seul hôte payé, pas un proxy payé devant une VM Oracle allumée à la demande
**Décidé :** tout sur un VPS payé au Québec. Le compte Oracle Always Free reste,
mais comme bac à sable d'apprentissage et cible de sauvegarde — jamais sur le
chemin du jeu.

**L'idée écartée :** un petit VPS payé qui garde Gate allumé en permanence et
démarre/arrête la VM ARM Oracle selon la présence de joueurs.

**Pourquoi ça ne marche pas :**
1. *Ça provoque exactement ce qu'on veut éviter.* Une instance Always Free
   arrêtée compte comme inactive. Le critère de récupération (CPU, réseau et
   mémoire tous sous 20 % sur 7 jours) est rempli en continu par une machine
   éteinte. On garantit la récupération au lieu de l'éviter.
2. *Le redémarrage est un pari.* Rallumer une instance ARM Always Free peut
   échouer en « Out of capacity » — le problème même qui rend Oracle incertain
   pour cet usage. Le serveur deviendrait indisponible de façon aléatoire, à
   chaque fois que quelqu'un veut jouer.
3. *Le délai est visible.* Démarrage d'instance (1-2 min) plus démarrage du
   serveur (~1 min) contre un redémarrage de conteneur (~40 s). Le joueur
   attend trois fois plus longtemps.

**Et surtout :** on paie déjà le VPS. Ajouter Oracle derrière n'économise rien —
ça ajoute un deuxième hôte, de la latence entre les deux, des identifiants d'API
OCI sur une machine exposée, et la loterie de capacité. Le gain est nul.

**Conséquence sur l'inventaire :** le groupe `edge` devient optionnel. Il reste
valide si le jeu tourne un jour sur Oracle, mais l'installation par défaut est
un seul hôte.

## 0009 — La pénurie de mémoire change l'arbitrage (constaté 2026-09-19)
**Contexte :** le prix au comptant de la DRAM a monté d'environ 700 % sur un an
(juillet 2026), les fondeurs ayant réaffecté leur capacité vers la HBM pour l'IA.
Les hébergeurs ont répercuté : Hetzner +30-38 % en avril 2026, la gamme VPS 2026
d'OVH +40 à 67 %, Netcup et plusieurs autres ont ajusté. TrendForce attend une
pression soutenue jusqu'en 2028, certaines analyses jusqu'en 2030.

**Ce que ça implique pour ce projet :**

1. *Minecraft est une charge gourmande en RAM, au pire moment.* Le tas Java est
   la ligne de coût dominante sur un VPS payé aujourd'hui.
2. *Les 12 Go gratuits d'Oracle sont un actif qui prend de la valeur.* Une
   allocation figée à 0 $ pendant que le prix de marché de la RAM explose.
3. *Et ça explique probablement la coupe du 15 juin 2026* (4 OCPU / 24 Go →
   2 / 12, sans annonce). Donc **il faut s'attendre à ce qu'Oracle recoupe.**
   La portabilité du dépôt n'est pas de la paranoïa, c'est la réponse au
   mécanisme qui a déjà frappé une fois.
4. *La disparition de petits fournisseurs s'accélère* — marges comprimées. Le
   cas Elixior (site mort en moins d'un an après une offre 8 Go à 6,50 $ CA) est
   le symptôme, pas une exception. Ne pas prépayer à l'année chez un petit
   acteur.

**Arbitrage inversé selon l'hôte, à retenir :**
- **Sur Oracle Always Free** : réserver de la mémoire est *souhaitable*. Le
  `-Xms4G` sur 12 Go maintient 33 % d'utilisation et empêche la récupération
  pour inactivité. La RAM ne coûte rien.
- **Sur un VPS payé** : la mémoire est la ligne la plus chère. Le tas descend à
  3 Go, FerriteCore et une `simulation-distance` réduite deviennent des
  décisions budgétaires, pas seulement des réglages de performance.

**Décision :** Oracle en premier, et la règle d'arrêt de trois semaines est
levée. L'écart de valeur justifie d'attendre plus longtemps.
