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
- ~~**Bastion**~~ — **ANNULÉ le 2026-09-19 par l'ADR 0010.** Les sessions Bastion
  se connectent à leur endpoint sur le port 22, et le 22 SORTANT est bloqué sur
  le réseau de l'opérateur : le service serait provisionné, gratuit, et
  injoignable. Remplacé par un 22 ouvert au CIDR du VCN seulement, joint depuis
  Cloud Shell attaché au VCN.
- **Functions + Notifications** — sonde externe de disponibilité. Une machine ne
  peut pas surveiller sa propre panne ; c'est le seul rôle qui *exige* d'être
  ailleurs. **PAS ENCORE IMPLÉMENTÉ** — c'est le trou le plus large de cette
  liste, et le seul élément retenu dont rien n'existe dans le dépôt.

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
3. *Et ça explique probablement la coupe du début de juin 2026* (4 OCPU /
   24 Go → 2 / 12, sans annonce). Donc **il faut s'attendre à ce qu'Oracle recoupe.**
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

## 0010 — Accès à l'hôte : `ansible-pull` sur 443, pas de bastion (2026-09-19)
**Décidé :** aucune ressource `oci_bastion_bastion`. Le port 22 est ouvert en
entrée **depuis le CIDR du VCN seulement**, jamais depuis Internet. Le
déploiement normal est `ansible-pull` — l'hôte va chercher le dépôt en HTTPS. Le
bris de glace est Cloud Shell attaché au VCN (« private network access »), puis
la console série de l'instance si le réseau de l'hôte est mort.

**Correction du 2026-09-19, même jour :** la première version de cet ADR disait
« aucun port 22 » ET « bris de glace par Cloud Shell ». C'était contradictoire :
Cloud Shell joint l'instance par son IP, donc à travers la Security List. Sans
règle 22, la machine démarrait injoignable par tout sauf la console série. Une
Security List et un accès hors-bande ne sont pas la même couche — seule la
console série passe par l'hyperviseur et ignore la Security List.
**Pourquoi :** le port 22 **sortant** est bloqué sur le réseau de l'opérateur —
VÉRIFIÉ : `github.com:22` et `host.bastion.ca-montreal-1.oci.oraclecloud.com:22`
refusent tous deux la connexion, alors que le 443 passe. Les sessions OCI Bastion
(managed-SSH comme port-forwarding) se connectent à leur endpoint sur le 22 : un
bastion serait provisionné, gratuit, et inutilisable. Le VPN Primat n'y change
rien — il est en split tunnel, l'IP sortante reste celle du FAI (VÉRIFIÉ
2026-09-19). Ansible en mode *push* depuis le portable est donc mort sur ce
réseau, ce qui promeut `ansible-pull` de « un jour » à mécanisme principal.
**Écarté :** le bastion OCI (gratuit mais injoignable d'ici, et il faudrait
encore vérifier le plugin Oracle Cloud Agent et les contraintes de sous-réseau) ;
ouvrir le 22 au monde dans la Security List (expose la surface *et* ne marche
toujours pas d'ici, le blocage étant sortant).
**Conséquences à ne pas oublier :**
- `ansible-pull` tirera d'une branche `deploy`, jamais de `main` — le dépôt est
  public, une poussée irait droit en production.
- **GitHub n'est PAS concerné par le blocage du 22.** `ssh.github.com` écoute sur
  le **443** ; `~/.ssh/config` redirige déjà `github.com` vers lui, et
  `ssh -T git@github.com` répond. Donc `git push` en SSH fonctionne d'ici, et
  `origin` reste en SSH. Le blocage du 22 vaut pour OCI Bastion, dont l'endpoint
  n'offre aucune alternative sur 443 — c'est ce qui le rend inutilisable, pas une
  impossibilité générale de faire du SSH depuis ce réseau. Basculer `origin` en
  HTTPS « pour contourner » est une fausse bonne idée : le jeton OAuth de `gh`
  refuse alors de pousser toute modification de `.github/workflows/`, faute de
  portée `workflow`. Essayé le 2026-09-19, annulé.
- **La console série exige une clé RSA** — ed25519 est refusé (VÉRIFIÉ dans la
  doc Oracle, la question traînait comme inconnue depuis plusieurs sessions).
  D'où `~/.ssh/caffelatte_console_rsa`, distincte de `caffelatte_ed25519`. Son
  proxy se connecte sur le **443**, donc le réseau de l'opérateur ne la bloque pas.
- Le réseau privé de Cloud Shell est **éphémère** : à réattacher à chaque session,
  ou sauvegarder une définition nommée (max 5).
- La doc Oracle ne confirme pas qu'un sous-réseau **public** est accepté comme
  cible du réseau privé Cloud Shell — elle ne l'interdit pas non plus. Si la
  Console refuse, le repli est un `/28` privé dédié, sans toucher au sous-réseau
  de l'instance.

## 0011 — OpenTofu, pas Terraform (2026-09-19)
**Décidé :** `tofu`, paquet officiel de Fedora (1.11.5). La CI utilise
`opentofu/setup-opentofu`.
**Pourquoi :** Fedora n'empaquette plus Terraform depuis le passage à la licence
BUSL (août 2023) — le seul choix géré par `dnf` est OpenTofu. Un binaire déposé
à la main dans `~/.local/bin` échappe à `dnf-automatic` et pourrit en silence,
ce qui est exactement ce que ce dépôt refuse ailleurs. Bonus mesuré, pas
supposé : le backend `s3` d'OpenTofu **n'envoie pas** l'encodage « aws-chunked »
qu'OCI rejette. Sous Terraform 1.16 il fallait `skip_s3_checksum = true` **plus**
`AWS_REQUEST_CHECKSUM_CALCULATION=when_required` et
`AWS_RESPONSE_CHECKSUM_VALIDATION=when_required` dans une enveloppe shell, sinon
tout `PutObject` (donc le verrou d'état) partait en 501. VÉRIFIÉ : sous OpenTofu,
`plan` prend et relâche le verrou sans aucun des trois. L'enveloppe est supprimée.
**Écarté :** ajouter le dépôt dnf de HashiCorp (garde la BUSL dans un dépôt
public destiné au portfolio, pour zéro gain fonctionnel ici) ; garder le binaire
non géré (dette qu'on aurait payée à la prochaine CVE du SDK AWS).
**Piège à retenir :** `init` ne fait que LIRE l'état. Un backend « initialisé
avec succès » peut être mort en écriture — le test réel est `plan` avec verrou.

## 0012 — Le provider OCI s'authentifie par jeton de session (2026-09-19)
**Décidé :** un bloc `provider "oci"` explicite dans `terraform/main.tf`, avec
`auth = "SecurityToken"`, `config_file_profile = "CaffeLatte"` et `region`.
**Pourquoi :** il n'y avait aucun bloc `provider`. Le provider retombait alors
sur son défaut — l'authentification par clé d'API du profil `DEFAULT` — et le
compte n'a pas de clé d'API : il s'ouvre par `oci session authenticate`. Chaque
appel répondait `401-NotAuthenticated`, sur TOUS les services à la fois.
La `region` est obligatoire dans le bloc : en mode `SecurityToken` le provider
ne la lit pas dans `~/.oci/config` et échoue sur « can not get region from
Terraform configuration ».
**Piège à retenir :** ce trou a survécu cinq sessions parce que rien ne le
touchait. La CI ne fait que `fmt -check` et `validate`, deux commandes qui
n'appellent jamais l'API du fournisseur ; et `plan` sur un état vide n'en
appelle presque pas. Le premier vrai contact avec l'authentification, c'est
`apply`. Même famille que le piège de l'ADR 0011 (`init` ne fait que lire), un
étage plus haut — et les deux chemins d'authentification sont disjoints : le
backend d'état passe par une Customer Secret Key, le provider par le jeton.
**Deuxième profil, à clé d'API (`CaffeLatteAuto`) :** la boucle d'attente de
capacité ne peut pas dépendre d'un jeton qui meurt en une heure. `auth_oci` et
`profil_oci` laissent choisir ; une `validation` refuse un mode incohérent, les
deux étant incompatibles (l'un exige un fichier de jeton, l'autre une empreinte
et une clé privée).

**Piège vérifié aujourd'hui :** une clé d'API fraîchement téléversée met
quelques minutes à devenir utilisable, et l'erreur est le même
`401-NotAuthenticated` qu'une configuration fausse. MESURÉ : échec à 3 min après
le téléversement, succès à ~7 min, sans rien changer d'autre. C'est exactement
le délai déjà constaté sur la Customer Secret Key (ADR 0011) — même piège, autre
type de clé. Corollaire testé explicitement pour ne pas inscrire une fausse
leçon : les lignes de commentaire dans `~/.oci/config` ne gênent PAS le provider,
l'hypothèse a été rejetée en remettant un commentaire et en replanifiant.

**Conséquence opérationnelle :** le jeton expire après une heure. Une session de
travail qui dépasse ça doit relancer `oci session authenticate --profile-name
CaffeLatte --region ca-montreal-1` — `oci session refresh` échoue une fois le
jeton périmé.

## 0013 — La boucle de capacité garde `tofu apply` ; pas de `oci launch` + `import` (2026-09-19)

**Décision : la boucle d'attente continue de lancer `tofu apply` quand la
capacité ARM se libère.** L'alternative envisagée depuis deux sessions — capturer
la capacité avec `oci compute instance launch`, puis récupérer l'instance par
`tofu import` — est refusée.

Ce qui la rendait tentante : `apply` prend le verrou d'état, et un verrou
orphelin a déjà coûté une nuit. `oci compute instance launch` ne prend rien.

Ce qui la condamne : elle demande de répliquer à la main les champs du bloc
`oci_core_instance` (forme, `shape_config`, `source_details`,
`create_vnic_details`, `metadata`, `agent_config`, et depuis aujourd'hui
`is_pv_encryption_in_transit_enabled` et `instance_options`). Un seul champ
divergent et le `plan` qui suit l'`import` propose de **remplacer** l'instance —
donc de relâcher la capacité ARM qu'on vient d'attendre des semaines. Le remède
détruit précisément ce qu'il protège, et le fait au pire moment.

**Le fait qui tranche, vérifié aujourd'hui :** le verrou n'est pas un péage
permanent. Les lectures se font sous `-lock=false` (`tofu plan -lock=false` a
servi à valider le durcissement de l'instance pendant que la boucle tournait,
sans la déranger). Le verrou n'est donc pris que par l'`apply` de la boucle,
c'est-à-dire exactement le moment où il doit l'être — une opération qui écrit
mérite son verrou. Le risque résiduel se réduit à un `apply` interrompu, qui se
répare par un `force-unlock` documenté, là où un `import` raté se répare en
attendant à nouveau la capacité.

**Corollaire pour le travail courant :** toute inspection de l'état pendant que
la boucle tourne passe par `-lock=false`. Ce n'est pas un contournement, c'est
la lecture correcte.
