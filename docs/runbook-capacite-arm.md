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

## Ce qu'il faut attendre, honnêtement

La capacité A1 gratuite à Montréal revient par vagues courtes, souvent la nuit,
et elle part vite. Relancer à la main pendant une session de travail n'attrape
rien — ceux qui l'obtiennent laissent une boucle tourner des jours. Le portable
n'étant pas allumé en permanence, la boucle ne couvre que les heures où il
tourne : c'est la limite acceptée, pas un défaut du script.

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
