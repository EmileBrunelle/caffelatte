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
# PRÉREQUIS : un profil OCI à clé d'API, PAS un jeton de session. Un jeton
# expire au bout d'une heure et ne se renouvelle pas sans navigateur, ce qui
# tue la boucle en pleine nuit. Le profil est repris de $OCI_PROFIL.
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
JOURNAL="$REPO/capacite-arm.log"   # gitignoré
trap 'rm -f "$PLAN" "$RAPPORT"' EXIT

# Toutes les 5 minutes, jamais plus vite : OCI limite le débit des requêtes et
# un martèlement peut faire bloquer le compte.
INTERVALLE=300

# La sonde est en lecture seule, donc une panne passagère (réseau, 5xx) ne doit
# PAS tuer la nuit. Mais une panne durable (clé supprimée, profil cassé) doit
# s'arrêter au lieu de sonder dans le vide jusqu'au matin.
ECHECS_SONDE_MAX=5

journal() { printf '%s  %s\n' "$(date '+%F %T')" "$1" | tee -a "$JOURNAL"; }

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
    --profile "$PROFIL" \
    --shape-availabilities "[{\"instanceShape\":\"VM.Standard.A1.Flex\",\
\"instanceShapeConfig\":{\"ocpus\":$OCPUS,\"memoryInGBs\":$MEMOIRE}}]" \
    >"$RAPPORT" 2>&1
}

creer_instance() {
  cd "$TF" || return 1
  tofu plan -no-color -input=false -var-file=terraform.tfvars \
    -var "profil_oci=$PROFIL" -var "auth_oci=ApiKey" -out="$PLAN" 2>&1 &&
    tofu apply -no-color -input=false "$PLAN" 2>&1
}

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

  if [ $? -eq 0 ]; then
    journal "SUCCÈS à la tentative $tentative. L'instance existe."
    printf '%s\n' "$sortie" | grep -E 'Creation complete|Apply complete' >>"$JOURNAL"
    journal "Suite : docs/runbook-capacite-arm.md, puis vérifier les deux accès."
    exit 0
  fi

  # La fenêtre s'est refermée pendant l'apply : ça arrive, on continue.
  if printf '%s\n' "$sortie" | grep -q "Out of host capacity"; then
    journal "Fenêtre refermée pendant la création. On continue de sonder."
    sleep "$INTERVALLE"
    continue
  fi

  # Une erreur qui n'est PAS un manque de capacité doit arrêter la boucle :
  # sinon on martèle l'API pendant des jours sur un bug de configuration.
  journal "ARRÊT : erreur autre qu'un manque de capacité."
  printf '%s\n' "$sortie" | tail -30 | tee -a "$JOURNAL"
  exit 2
done
