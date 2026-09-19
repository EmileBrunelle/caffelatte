# Obtenir la VM ARM à Montréal

`ca-montreal-1` n'a **qu'un seul domaine de disponibilité** : le conseil courant
« essaie un autre AD » ne s'applique pas. Et la région d'origine est fixée à
l'inscription — en changer est une demande de service, pas une case à cocher.
Donc : tester la capacité *avant* de construire quoi que ce soit dessus.

Depuis le 15 juin 2026 le maximum gratuit est **2 OCPU / 12 Go** (contre 4 / 24
avant). Ne pas demander 4 OCPU : la requête échoue pour dépassement de quota,
pas pour manque de capacité, et le message ne le distingue pas clairement.

## Test unique

    oci compute instance launch --shape VM.Standard.A1.Flex \
      --shape-config '{"ocpus":2,"memoryInGBs":12}' \
      --availability-domain "$AD" --compartment-id "$C" \
      --image-id "$IMG" --subnet-id "$SUBNET" --display-name core

- `Out of capacity for shape VM.Standard.A1.Flex` → capacité absente maintenant.
  Elle se libère par vagues ; réessayer périodiquement fonctionne.
- `LimitExceeded` → quota, pas capacité. Réduire à 2 OCPU / 12 Go.

## Boucle de réessai

Toutes les 5 minutes, pas plus vite : OCI limite le débit des requêtes et un
martèlement peut faire bloquer le compte.

    until oci compute instance launch ... 2>/tmp/err; do
      grep -q "Out of capacity" /tmp/err || { cat /tmp/err; break; }  # vraie erreur : arrêter
      sleep 300
    done

## Si Montréal ne donne rien

Par ordre de préférence :
1. Passer en Pay As You Go (les ressources Always Free le restent, mais la
   capacité est priorisée). C'est le levier qui marche le plus souvent.
2. Demander le changement de région d'origine vers `ca-toronto-1`.
3. Rabattre sur un fournisseur ARM tiers. Rien dans ce dépôt n'est spécifique à
   OCI (ADR 0005) : `ansible-playbook site.yml` contre un hôte EL suffit.
