#!/bin/bash
# Attendre que la capacité A1 se libère à Montréal, puis créer l'instance.
#
# La capacité ARM gratuite revient par vagues courtes, souvent la nuit. Personne
# ne l'attrape en relançant à la main : ça se prend avec une boucle qui tourne
# des jours. Voir docs/runbook-capacite-arm.md.
#
#   ./scripts/attendre-capacite-arm.sh
#
# Ctrl-C pour arrêter. Relancer est sans danger : OpenTofu reprend là où l'état
# en est, et une instance déjà créée n'est pas recréée.
#
# PRÉREQUIS sur le portable : un profil OCI à clé d'API, PAS un jeton de
# session. Un jeton expire au bout d'une heure et ne se renouvelle pas sans
# navigateur, ce qui tue la boucle en pleine nuit. Le profil vient de $OCI_PROFIL.
#
# Sur la veilleuse (la micro x86 qui veille à notre place), il n'y a ni profil
# ni clé : l'identité vient de l'instance. Le service systemd y pose
#   OCI_AUTH=instance_principal, CAFFELATTE_NOTIFIER, CAFFELATTE_JOURNAL.
#
# SONDE : CreateComputeCapacityReport, pas `tofu apply`. Mesuré le 2026-09-19 :
# l'API de capacité répond la même chose que l'apply (OUT_OF_HOST_CAPACITY),
# mais en lecture seule — donc SANS prendre le verrou d'état. L'ancienne boucle
# le prenait à chaque plan et à chaque apply; une interruption en pleine nuit
# laissait un verrou orphelin et la boucle abandonnait jusqu'au matin. `tofu`
# n'est maintenant réveillé qu'au moment où la capacité existe vraiment : le
# verrou n'est plus sur le chemin d'attente, seulement sur le chemin décisif.

set -u

PROFIL="${OCI_PROFIL:-CaffeLatteAuto}"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TF="$REPO/terraform"
PLAN="$(mktemp -t caffelatte-plan.XXXXXX)"
RAPPORT="$(mktemp -t caffelatte-capacite.XXXXXX)"
# Sur la veilleuse le journal vit hors du dépôt cloné, qui est jetable.
JOURNAL="${CAFFELATTE_JOURNAL:-$REPO/capacite-arm.log}"   # gitignoré

# Toutes les 5 minutes, jamais plus vite : OCI limite le débit des requêtes et
# un martèlement peut faire bloquer le compte.
INTERVALLE=300

# La sonde est en lecture seule, donc une panne passagère (réseau, 5xx) ne doit
# PAS tuer la nuit. Mais une panne durable (clé supprimée, profil cassé) doit
# s'arrêter au lieu de sonder dans le vide jusqu'au matin.
ECHECS_SONDE_MAX=5

journal() { printf '%s  %s\n' "$(date '+%F %T')" "$1" | tee -a "$JOURNAL"; }

# Prévenir par courriel n'a de sens que là où personne ne lit le journal, donc
# uniquement sur la veilleuse : sans $CAFFELATTE_NOTIFIER, c'est un no-op.
notifier() {
  [ -n "${CAFFELATTE_NOTIFIER:-}" ] || return 0
  "$CAFFELATTE_NOTIFIER" "$1" "$2" >>"$JOURNAL" 2>&1 ||
    journal "AVERTISSEMENT : la notification n'est pas partie."
}

# Un arrêt DOIT se voir : sur la veilleuse, une boucle morte et une boucle qui
# attend produisent le même silence. D'où l'alerte sur toutes les sorties non
# nulles, pas seulement sur celles qu'on a pensé à instrumenter.
#
# Mais le service y redémarre toutes les minutes : une panne durable enverrait
# un courriel par minute. Une heure entre deux alertes suffit à ne rien rater,
# et le battement hebdomadaire rattrape le reste.
ETRANGLOIR="${TMPDIR:-/tmp}/caffelatte-derniere-alerte"
fin() {
  code=$?
  rm -f "$PLAN" "$RAPPORT"
  # Sur le portable il n'y a personne à prévenir : le journal est sous les yeux.
  [ -n "${CAFFELATTE_NOTIFIER:-}" ] || return 0
  [ "$code" -eq 0 ] && return 0   # le succès a déjà prévenu, avec son détail
  if [ -f "$ETRANGLOIR" ] &&
    [ "$(($(date +%s) - $(stat -c %Y "$ETRANGLOIR")))" -lt 3600 ]; then
    journal "Alerte étouffée : une autre est partie il y a moins d'une heure."
    return 0
  fi
  : >"$ETRANGLOIR"
  notifier "caffelatte : la veille s'est arrêtée (code $code)" \
    "La boucle d'attente de capacité ARM s'est arrêtée avec le code $code.

Fin du journal :
$(tail -20 "$JOURNAL" 2>/dev/null)"
}
trap fin EXIT

# Deux identités possibles, sans rien en commun :
#   api_key            — le portable, un profil de ~/.oci/config ;
#   instance_principal — la veilleuse, qui ne porte AUCUNE clé. Son identité
#                        vient de l'instance et se révoque en retirant la
#                        policy, sans toucher à la machine.
AUTH="${OCI_AUTH:-api_key}"
case "$AUTH" in
instance_principal)
  OCI_ARGS=(--auth instance_principal)
  TF_ARGS=(-var "auth_oci=InstancePrincipal")
  ;;
api_key)
  OCI_ARGS=(--profile "$PROFIL")
  TF_ARGS=(-var "profil_oci=$PROFIL" -var "auth_oci=ApiKey")
  grep -q "^\[$PROFIL\]" ~/.oci/config || {
    journal "ERREUR : profil [$PROFIL] absent de ~/.oci/config."
    journal "La boucle a besoin d'une clé d'API ; un jeton de session expire en 1 h."
    exit 1
  }
  # On lit la section du profil jusqu'au prochain en-tête, pas un nombre fixe de
  # lignes : un `grep -A5` rate un security_token_file repoussé plus bas par des
  # commentaires, et laisse alors la boucle démarrer sur un jeton qui expire en 1 h.
  awk -v p="[$PROFIL]" '$0==p{f=1;next} f&&/^\[/{exit} f' ~/.oci/config |
    grep -q security_token_file && {
    journal "ERREUR : [$PROFIL] est un profil à jeton de session, pas à clé d'API."
    exit 1
  }
  ;;
*)
  journal "ERREUR : OCI_AUTH=$AUTH inconnu (api_key ou instance_principal)."
  exit 1
  ;;
esac

# La veilleuse démarre sur un clone tout neuf : pas de `.terraform`, et surtout
# pas de backend.hcl ni de terraform.tfvars. Le premier porte la Customer Secret
# Key, que l'on refuse de mettre dans user_data — elle se dépose à la main, une
# fois (docs/runbook-capacite-arm.md). Tant qu'ils manquent on sort SANS
# prévenir : le redémarrage systemd toutes les minutes EST l'attente, et alerter
# là-dessus noierait les vraies pannes.
for fichier in backend.hcl terraform.tfvars; do
  [ -f "$TF/$fichier" ] || {
    journal "EN ATTENTE : terraform/$fichier n'est pas déposé."
    # Sortie 0, pas 1 : ce n'est pas une panne, c'est l'attente elle-même, et
    # `Restart=always` relance de toute façon. Le 0 évite d'alerter chaque heure.
    exit 0
  }
done
if [ ! -d "$TF/.terraform" ]; then
  journal "Premier démarrage : tofu init."
  (cd "$TF" && tofu init -no-color -input=false -backend-config=backend.hcl) >>"$JOURNAL" 2>&1 || {
    journal "ARRÊT : tofu init a échoué."
    exit 2
  }
fi

# La forme est lue dans main.tf, jamais recopiée : sonder une forme différente
# de celle qu'on crée donnerait un feu vert sur la mauvaise taille.
OCPUS=$(awk -F'=' '/^[[:space:]]*ocpus[[:space:]]*=/ {gsub(/[^0-9.]/,"",$2); print $2; exit}' "$TF/main.tf")
MEMOIRE=$(awk -F'=' '/^[[:space:]]*memory_in_gbs[[:space:]]*=/ {gsub(/[^0-9.]/,"",$2); print $2; exit}' "$TF/main.tf")
AD=$(awk -F'"' '/^[[:space:]]*availability_domain/ {print $2; exit}' "$TF/terraform.tfvars")
COMPARTIMENT=$(awk -F'"' '/^[[:space:]]*compartment_id/ {print $2; exit}' "$TF/terraform.tfvars")

[ -n "$OCPUS" ] && [ -n "$MEMOIRE" ] && [ -n "$AD" ] && [ -n "$COMPARTIMENT" ] || {
  journal "ERREUR : forme ou cible introuvable (ocpus=$OCPUS mem=$MEMOIRE ad=$AD)."
  journal "Vérifier terraform/main.tf et terraform/terraform.tfvars."
  exit 1
}

# La clé JSON est instanceShapeConfig. Avec shapeConfig, l'API répond 400
# « Cannot launch flexible instance without ShapeConfig » — trompeur, ça
# ressemble à une forme invalide alors que c'est le nom du champ qui est faux.
sonder_capacite() {
  oci compute compute-capacity-report create \
    --compartment-id "$COMPARTIMENT" \
    --availability-domain "$AD" \
    "${OCI_ARGS[@]}" \
    --shape-availabilities "[{\"instanceShape\":\"VM.Standard.A1.Flex\",\
\"instanceShapeConfig\":{\"ocpus\":$OCPUS,\"memoryInGBs\":$MEMOIRE}}]" \
    >"$RAPPORT" 2>&1
}

# La boucle applique l'ARBRE DE TRAVAIL, pas HEAD. Un .tf modifié et non
# commité au moment où la capacité se libère part donc en production à 3 h du
# matin sans que personne l'ait relu — sur la ressource la plus chère à
# reprendre du projet, puisque rater l'A1 se repaie en semaines d'attente.
# C'est arrivé de justesse le 2026-09-19 : du code d'une nouvelle instance
# traînait dans terraform/ pendant que la boucle tournait.
#
# Rater une vague se rattrape, la vague suivante arrive. Appliquer du non-relu
# ne se rattrape pas. D'où le refus, et non l'avertissement.
arbre_propre() {
  [ -z "$(git -C "$REPO" status --porcelain -- terraform/ 2>/dev/null)" ]
}

creer_instance() {
  if ! arbre_propre; then
    journal "REFUS D'APPLIQUER : terraform/ a des modifications non commitées."
    git -C "$REPO" status --porcelain -- terraform/ | tee -a "$JOURNAL"
    return 3
  fi
  cd "$TF" || return 1
  # L'apply est CIBLÉ sur l'A1 et son volume, jamais sur tout le fichier. La
  # veilleuse ne doit pouvoir faire, à 3 h du matin, QUE la chose pour laquelle
  # elle existe — c'est le même principe que le refus d'appliquer un arbre sali.
  # Accessoirement, sa policy ne lui donne aucun droit sur les ressources
  # d'identité : un refresh complet échouerait sur son propre groupe dynamique.
  # Les dépendances (VCN, sous-réseau, image) sont ciblées d'office par tofu.
  tofu plan -no-color -input=false -var-file=terraform.tfvars \
    "${TF_ARGS[@]}" \
    -target=oci_core_instance.core -target=oci_core_volume_attachment.donnees \
    -out="$PLAN" 2>&1 &&
    tofu apply -no-color -input=false "$PLAN" 2>&1
}

if ! arbre_propre; then
  journal "ARRÊT AU DÉMARRAGE : terraform/ a des modifications non commitées."
  git -C "$REPO" status --porcelain -- terraform/ | tee -a "$JOURNAL"
  journal "Commiter ou remiser avant de veiller : la boucle applique l'arbre de travail."
  exit 1
fi

journal "Début. Profil $PROFIL, forme ${OCPUS} OCPU / ${MEMOIRE} Go, sonde aux $((INTERVALLE / 60)) min."
tentative=0
echecs_sonde=0
while true; do
  tentative=$((tentative + 1))

  if ! sonder_capacite; then
    echecs_sonde=$((echecs_sonde + 1))
    journal "Sonde $tentative : échec d'appel ($echecs_sonde/$ECHECS_SONDE_MAX)."
    if [ "$echecs_sonde" -ge "$ECHECS_SONDE_MAX" ]; then
      journal "ARRÊT : la sonde échoue depuis $ECHECS_SONDE_MAX essais."
      tail -20 "$RAPPORT" | tee -a "$JOURNAL"
      exit 2
    fi
    sleep "$INTERVALLE"
    continue
  fi
  echecs_sonde=0

  statut=$(grep -o '"availability-status": *"[A-Z_]*"' "$RAPPORT" | head -1 | cut -d'"' -f4)

  if [ "$statut" != "AVAILABLE" ]; then
    journal "Sonde $tentative : ${statut:-REPONSE_ILLISIBLE}. Nouvelle sonde dans $((INTERVALLE / 60)) min."
    sleep "$INTERVALLE"
    continue
  fi

  # Capacité annoncée : c'est le seul moment où on réveille tofu, donc le seul
  # moment où le verrou d'état est pris. La fenêtre peut se refermer entre la
  # sonde et l'apply — dans ce cas on retombe dans la boucle sans drame.
  journal "Sonde $tentative : CAPACITÉ DISPONIBLE. Création en cours."
  sortie="$(creer_instance)"
  # Capturé TOUT DE SUITE : $? ne survit pas au premier test, et les branches
  # plus bas en ont besoin pour distinguer un refus d'un échec d'apply.
  code=$?

  if [ "$code" -eq 0 ]; then
    journal "SUCCÈS à la tentative $tentative. L'instance existe."
    printf '%s\n' "$sortie" | grep -E 'Creation complete|Apply complete' >>"$JOURNAL"
    journal "Suite : docs/runbook-capacite-arm.md, puis vérifier les deux accès."
    notifier "caffelatte : L'INSTANCE A1 EXISTE" \
      "La capacité ARM s'est libérée à la tentative $tentative et l'apply a réussi.

Suite dans docs/runbook-capacite-arm.md : pousser main:deploy, vérifier les deux
accès, puis détruire la veilleuse — son travail est fini.

$(printf '%s\n' "$sortie" | grep -E 'Creation complete|Apply complete')"
    exit 0
  fi

  # La fenêtre s'est refermée pendant l'apply : ça arrive, on continue.
  if printf '%s\n' "$sortie" | grep -q "Out of host capacity"; then
    journal "Fenêtre refermée pendant la création. On continue de sonder."
    sleep "$INTERVALLE"
    continue
  fi

  # Arbre sali PENDANT la veille : quelqu'un travaille dans terraform/. Arrêter
  # est le comportement utile — sonder pour refuser chaque fenêtre ne protège
  # rien et laisse croire que la veille tient.
  if [ "$code" -eq 3 ]; then
    journal "ARRÊT : arbre de travail sali pendant la veille."
    exit 3
  fi

  # Une erreur qui n'est PAS un manque de capacité doit arrêter la boucle :
  # sinon on martèle l'API pendant des jours sur un bug de configuration.
  journal "ARRÊT : erreur autre qu'un manque de capacité."
  printf '%s\n' "$sortie" | tail -30 | tee -a "$JOURNAL"
  exit 2
done
