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

set -u

PROFIL="${OCI_PROFIL:-CaffeLatteAuto}"
REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TF="$REPO/terraform"
PLAN="$(mktemp -t caffelatte-plan.XXXXXX)"
JOURNAL="$REPO/capacite-arm.log"   # gitignoré
trap 'rm -f "$PLAN"' EXIT

# Toutes les 5 minutes, jamais plus vite : OCI limite le débit des requêtes et
# un martèlement peut faire bloquer le compte.
INTERVALLE=300

journal() { printf '%s  %s\n' "$(date '+%F %T')" "$1" | tee -a "$JOURNAL"; }

grep -q "^\[$PROFIL\]" ~/.oci/config || {
  journal "ERREUR : profil [$PROFIL] absent de ~/.oci/config."
  journal "La boucle a besoin d'une clé d'API ; un jeton de session expire en 1 h."
  exit 1
}
grep -A5 "^\[$PROFIL\]" ~/.oci/config | grep -q security_token_file && {
  journal "ERREUR : [$PROFIL] est un profil à jeton de session, pas à clé d'API."
  exit 1
}

journal "Début. Profil $PROFIL, une tentative aux $((INTERVALLE / 60)) min."
tentative=0
while true; do
  tentative=$((tentative + 1))
  sortie="$(
    cd "$TF" || exit 1
    tofu plan -no-color -input=false -var-file=terraform.tfvars \
      -var "profil_oci=$PROFIL" -var "auth_oci=ApiKey" -out="$PLAN" 2>&1 &&
      tofu apply -no-color -input=false "$PLAN" 2>&1
  )"
  code=$?

  if [ $code -eq 0 ]; then
    journal "SUCCÈS à la tentative $tentative. L'instance existe."
    printf '%s\n' "$sortie" | grep -E 'Creation complete|Apply complete' >>"$JOURNAL"
    journal "Suite : docs/runbook-capacite-arm.md, puis vérifier les deux accès."
    exit 0
  fi

  # Une erreur qui n'est PAS un manque de capacité doit arrêter la boucle :
  # sinon on martèle l'API pendant des jours sur un bug de configuration.
  if ! printf '%s\n' "$sortie" | grep -q "Out of host capacity"; then
    journal "ARRÊT : erreur autre qu'un manque de capacité."
    printf '%s\n' "$sortie" | tail -30 | tee -a "$JOURNAL"
    exit 2
  fi

  journal "Tentative $tentative : pas de capacité. Nouvel essai dans $((INTERVALLE / 60)) min."
  sleep "$INTERVALLE"
done
