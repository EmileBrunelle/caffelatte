# Obtenir la VM ARM à Montréal

`ca-montreal-1` n'a **qu'un seul domaine de disponibilité** : le conseil courant
« essaie un autre AD » ne s'applique pas. Et la région d'origine est fixée à
l'inscription — en changer est une demande de service, pas une case à cocher.
Donc : tester la capacité *avant* de construire quoi que ce soit dessus.

Depuis le début de juin 2026 le maximum gratuit est **2 OCPU / 12 Go** (contre
4 / 24 avant). Ne pas demander 4 OCPU : la requête échoue pour dépassement de
quota, pas pour manque de capacité, et le message ne le distingue pas clairement.

La date vient des archives de la page officielle des limites Always Free, seule
preuve primaire trouvée : la capture du **2026-06-05 11:22 UTC** dit encore
« 4 OCPUs and 24 GB », celle du **2026-06-12 14:42 UTC** dit « 2 OCPUs and
12 GB ». Aucune capture entre les deux, donc la fenêtre ne se resserre pas
davantage. Les dates du 15 juin et du 18 août qui circulent sont toutes deux
fausses — ne pas les réintroduire.

    https://web.archive.org/web/20260605112238/https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm

## Test unique

    oci compute instance launch --shape VM.Standard.A1.Flex \
      --shape-config '{"ocpus":2,"memoryInGBs":12}' \
      --availability-domain "$AD" --compartment-id "$C" \
      --image-id "$IMG" --subnet-id "$SUBNET" --display-name core

- `Out of capacity for shape VM.Standard.A1.Flex` → capacité absente maintenant.
  Elle se libère par vagues ; réessayer périodiquement fonctionne.
- `LimitExceeded` → quota, pas capacité. Réduire à 2 OCPU / 12 Go.

## Boucle de réessai

Elle tourne **détachée**, pour survivre à la fermeture du terminal — les vagues
de capacité passent surtout la nuit :

    systemd-run --user --unit=caffelatte-arm --property=Restart=always \
      --property=RestartSec=60 \
      --working-directory=$HOME/Projets/caffelatte \
      $HOME/Projets/caffelatte/scripts/attendre-capacite-arm.sh

    systemctl --user status caffelatte-arm   # doit dire « active (running) »
    tail -f capacite-arm.log
    systemctl --user stop caffelatte-arm     # pour l'arrêter

Pour qu'elle survive aussi à une déconnexion : `sudo loginctl enable-linger
ebrunelle`. La veille du portable la suspend de toute façon.

Toutes les 5 minutes, pas plus vite : OCI limite le débit des requêtes et un
martèlement peut faire bloquer le compte. Le script s'arrête tout seul sur une
erreur qui n'est PAS un manque de capacité — sinon on martèle l'API pendant des
jours sur une faute de configuration. Relancer est sans danger.

**Ce qui boucle est `CreateComputeCapacityReport`**, une sonde en lecture seule,
pas `tofu`. `tofu apply` n'est réveillé que quand la sonde annonce de la
capacité. La raison est concrète : chaque `plan` comme chaque `apply` prend le
verrou d'état ; une interruption nocturne laissait un verrou orphelin et la
boucle abandonnait jusqu'au matin — c'est arrivé et ça a coûté une nuit. Le
verrou n'est plus sur le chemin d'attente, seulement sur le chemin décisif.

    oci compute compute-capacity-report create --compartment-id "$C" \
      --availability-domain "$AD" --profile CaffeLatteAuto \
      --shape-availabilities '[{"instanceShape":"VM.Standard.A1.Flex",
        "instanceShapeConfig":{"ocpus":2.0,"memoryInGBs":12.0}}]'

La clé JSON est `instanceShapeConfig`, **pas** `shapeConfig` : l'autre renvoie un
400 « Cannot launch flexible instance without ShapeConfig », trompeur.

La création, elle, passe par OpenTofu, jamais par `oci compute instance launch`
ni par l'interface web : une instance créée hors de l'état serait invisible pour
`tofu`, qui en recréerait une deuxième. L'interface web n'aide pas non plus la
capacité — elle appelle la même API et reçoit la même erreur (mesuré le
2026-09-19 : la sonde, qui contourne Terraform entièrement, reçoit le même
`OUT_OF_HOST_CAPACITY`).

**Si un verrou orphelin réapparaît** (« Error acquiring the state lock », la
boucle s'arrête en une seconde) : vérifier qu'aucun `tofu` ne tourne
(`pgrep -af tofu`), puis `cd terraform && tofu force-unlock <ID>`.

**Il lui faut un profil OCI à clé d'API**, pas à jeton de session : un jeton
expire au bout d'une heure et ne se renouvelle pas sans navigateur, donc la
boucle mourrait la nuit, précisément quand les vagues de capacité passent.

    oci setup keys --key-name caffelatte_auto
    oci iam user api-key upload --user-id <ocid de l'utilisateur> \
      --key-file ~/.oci/caffelatte_auto_public.pem

puis un profil `[CaffeLatteAuto]` dans `~/.oci/config` avec `user`, `fingerprint`,
`tenancy`, `region` et `key_file`. La clé d'API est un secret de longue durée sur
le portable : la supprimer une fois l'instance obtenue.

## La veilleuse : la même boucle, sur une machine qui ne dort pas

Le portable n'est pas allumé la nuit, précisément quand les vagues passent. La
boucle tourne donc aussi sur une **micro x86 gratuite** (`VM.Standard.E2.1.Micro`,
quota distinct de l'A1 — ce n'est PAS une deuxième A1), créée par le même
`tofu apply` que le reste. Elle est **jetable** : la détruire et la relancer
coûte deux minutes, d'où un amorçage entièrement en cloud-init
(`terraform/cloud-init-veilleuse.yaml`), sans Ansible ni branche `deploy`.

Le même script y tourne, avec trois variables posées par le service systemd :

    OCI_AUTH=instance_principal    # aucune clé d'API sur la machine
    CAFFELATTE_NOTIFIER=...        # publie sur le sujet ONS
    CAFFELATTE_JOURNAL=...         # hors du dépôt cloné, qui est jetable

**Elle s'authentifie en principal d'instance**, pas avec une clé déposée : rien
à voler sur la machine, et l'accès se révoque en retirant la policy sans y
toucher. La règle du groupe dynamique cible l'instance *précise*, donc l'A1
n'hérite jamais de ces droits.

**L'apply nocturne est ciblé** (`-target` sur l'A1 et son volume) : la veilleuse
ne peut créer que ce pour quoi elle existe, même si le dépôt cloné contient
autre chose. C'est le pendant du refus d'appliquer un arbre non commité, et ça
évite qu'un refresh complet bute sur les ressources d'identité, que sa policy ne
lui laisse pas lire. Après le succès, on reprend un `tofu apply` normal depuis
le portable.

### Sa config, qu'elle va chercher elle-même

`backend.hcl` contient la Customer Secret Key, qui ouvre le bucket d'état **et**
celui des sauvegardes. La mettre dans `user_data` la graverait dans l'état
OpenTofu et dans la console OCI. Elle vit donc dans le bucket privé, sous
`veilleuse/`, et la veilleuse l'y prend au premier démarrage avec son principal
d'instance — sa policy ne lui donne la lecture que de ce bucket-là.

Rien à déposer à la main. Pour mettre à jour la config, on repousse l'objet et
on recrée l'instance :

    oci os object put --namespace "$NS" --bucket-name caffelatte-backup \
      --name veilleuse/backend.hcl --file terraform/backend.hcl --force
    oci os object put --namespace "$NS" --bucket-name caffelatte-backup \
      --name veilleuse/terraform.tfvars --file terraform/terraform.tfvars --force

**La première version passait par « Run Command » depuis la console. Ne pas y
revenir :** mesuré le 2026-09-19, une commande y reste bloquée en `ACCEPTED`
indéfiniment alors que le greffon « Compute Instance Run Command » se déclare
`RUNNING`. Sans port 22, il ne restait alors aucun canal — la machine était
inatteignable.

### Savoir si l'amorçage a marché

La veilleuse est aveugle : pas de port 22, pas de Run Command. Elle **rend donc
compte d'elle-même** en fin de cloud-init, par un courriel « la veilleuse est en
poste » ou « amorçage INCOMPLET » qui nomme ce qui manque. Si aucun des deux
n'arrive, le CLI lui-même a échoué et le seul canal restant est le journal de
console, lisible depuis le portable :

    oci compute console-history capture --instance-id "$I"
    oci compute console-history get-content --instance-console-history-id "$H" \
      --length 200000 --file -

C'est ainsi qu'a été trouvée la panne du premier essai : `dnf install oci-cli`
n'existe pas sur Oracle Linux 10 (« No match for argument: oci-cli ») et le
repli `pip3` non plus. On passe maintenant par le script d'installation officiel
d'Oracle, qui ne dépend d'aucun dépôt.

### Les courriels

**Confirmer l'abonnement ONS** : OCI envoie un courriel de confirmation à la
création et ne livre **rien** tant que le lien n'est pas cliqué. Tant que
l'abonnement est `PENDING`, la veilleuse veille dans le vide.

Trois courriels possibles :

- **succès** — l'A1 existe ; suite ci-dessous ;
- **arrêt** — sortie non nulle de la boucle, avec la fin du journal. Étranglé à
  un par heure : le service redémarre à la minute, et une panne durable enverrait
  autrement un courriel par minute ;
- **battement hebdomadaire**, le lundi. Il n'est pas décoratif : une veilleuse
  récupérée par Oracle pour inactivité s'arrête dans un silence parfait,
  identique à celui d'une veille qui se passe bien. Le battement est le seul
  moyen de distinguer « rien à signaler » de « plus personne n'écoute ». **S'il
  cesse d'arriver, la veilleuse est morte** : la recréer par `tofu apply`.

### Recréer la veilleuse : jamais l'instance toute seule

La règle du groupe dynamique épingle **l'OCID exact de l'instance**
(`matching_rule = ALL {instance.id = '...'}`), délibérément, pour que l'A1
n'hérite jamais de ces droits. Conséquence : une instance recréée est une
instance **sans identité** tant que le groupe pointe sur l'ancienne. Toujours
les deux dans le même apply :

    tofu apply -var profil_oci=CaffeLatteAuto -var auth_oci=ApiKey \
      -target=oci_core_instance.veilleuse \
      -target=oci_identity_dynamic_group.veilleuse

Le symptôme, si on l'oublie : `head_object ... status 404` sur
`veilleuse/backend.hcl` dans le journal de console, alors que l'objet est bien
dans le bucket. Object Storage répond **404 et non 403** à un principal non
autorisé — il ne révèle pas l'existence de l'objet. Chercher une faute de nom
d'objet est donc une fausse piste ; c'est l'IAM. Mesuré le 2026-09-20.

La propagation IAM peut dépasser les dix essais du cloud-init. Ce n'est plus
fatal depuis que `ExecStartPre=-/usr/local/sbin/caffelatte-config` redemande la
config à chaque redémarrage du service, soit toutes les minutes.

### Une veilleuse qui s'amorce bien et ne sonde jamais

Symptôme, mesuré le 2026-09-20 : `AMORCAGE COMPLET` dans le journal de console,
service démarré, et **zéro `CreateComputeCapacityReport` par le principal
d'instance** dans le journal d'audit pendant des heures. La commande qui tranche
— une sonde par un principal AUTRE qu'« Émile Brunelle » veut dire que la
veilleuse travaille :

    oci audit event list --all --compartment-id "$C" \
      --start-time <il y a 30 min> --end-time <maintenant> \
      --query 'data[].{n:data."event-name",p:data.identity."principal-name"}'

Attention à deux choses en lisant ce journal : **il a environ 15 minutes de
retard**, et sans `--all` il ne rend que la première page. Compter par principal
ET par cadence : la boucle du portable sonde aux 5 minutes, donc 12 sondes à
l'heure toutes attribuées à « Émile Brunelle » ne prouvent rien sur la veilleuse.

La cause était que **`tofu init` réécrit `terraform/.terraform.lock.hcl`**, ce
qui salit `terraform/` — et le garde-fou d'arbre propre, qui s'exécute juste
après dans le même script, refuse alors de démarrer. Exit 1, redémarrage par
systemd, même chose, indéfiniment. Corrigé par `-lockfile=readonly` sur l'init.
Deux mécanismes corrects qui se mordent : à garder en tête avant d'ajouter quoi
que ce soit d'autre qui écrive dans `terraform/`.

### Une fois l'A1 obtenue

Le travail de la veilleuse est fini. La détruire — elle consomme une des deux
micro gratuites et porte une copie de la Customer Secret Key :

    tofu destroy -target=oci_core_instance.veilleuse \
      -target=oci_identity_dynamic_group.veilleuse \
      -target=oci_identity_policy.veilleuse

Le sujet ONS et son abonnement, eux, se gardent : ils ne coûtent rien et
serviront à la prochaine alerte.

## Ce qu'il faut attendre, honnêtement

La capacité A1 gratuite à Montréal revient par vagues courtes, souvent la nuit,
et elle part vite. Relancer à la main pendant une session de travail n'attrape
rien — ceux qui l'obtiennent laissent une boucle tourner des jours. C'est
exactement ce que la veilleuse répare : sur le portable seul, la boucle ne
couvrait que les heures où il était allumé, et ratait donc les vagues de nuit.

## Si Montréal ne donne rien

Par ordre de préférence :
1. Continuer à réessayer. La capacité revient par vagues ; c'est le cas le plus
   fréquent et il ne coûte que de la patience.
2. Demander le changement de région d'origine vers `ca-toronto-1`.
3. Rabattre sur un fournisseur ARM tiers. Rien dans ce dépôt n'est spécifique à
   OCI (ADR 0005) : `ansible-playbook site.yml` contre un hôte EL suffit.

**Pas Pay As You Go.** Et ce n'est pas un sacrifice : rien n'étaye l'idée que
PAYG débloque la capacité. Le cas le mieux daté (xxlsteve.net, 2025-05-04) est
un utilisateur qui paie ~93 € pour convertir *dans ce but précis* et reçoit la
même erreur ; il obtient sa capacité trois semaines plus tard, ce qui ressemble
à de la chance, pas à un effet de la conversion. Oracle ne documente aucune
priorité par palier. La décision tient donc sur sa propre raison : sur un compte
non converti, une ressource payante échoue au lieu de facturer, et cette
garantie est le seul coupe-circuit qui existe — les budgets d'OCI ne font que notifier, ils ne coupent rien. La
conversion est IRRÉVERSIBLE. Voir `docs/couts.md`.

## Une seule instance A1, jamais deux

La FAQ Always Free est explicite : au-delà du quota A1 d'une tenancy gratuite,
**toutes** les instances A1 existantes sont désactivées puis supprimées après 30
jours. Oracle ne réduit pas, il coupe tout. À 2 OCPU / 12 Go on est pile à la
limite : conforme, mais **jamais une deuxième A1**, même minuscule, même
temporaire. `oci limits value list --all` affiche des limites bien plus hautes
(16 OCPU régionaux) parce que le compte est en essai à crédits — rien
n'empêche techniquement de dépasser, le seul garde-fou est la discipline.

## Le bucket d'état est le point unique de défaillance

`caffelatte-tfstate` contient l'état Terraform et n'est **pas** géré par `tofu`
(un backend ne peut pas se gérer lui-même). S'il disparaissait, l'état serait
perdu et toutes les ressources deviendraient orphelines : visibles dans la
console, invisibles pour `tofu`. Ne pas le supprimer en faisant le ménage.
