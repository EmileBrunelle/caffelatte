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

    ./scripts/attendre-capacite-arm.sh

Toutes les 5 minutes, pas plus vite : OCI limite le débit des requêtes et un
martèlement peut faire bloquer le compte. Le script s'arrête tout seul sur une
erreur qui n'est PAS un manque de capacité — sinon on martèle l'API pendant des
jours sur une faute de configuration. `Ctrl-C` pour arrêter, relancer est sans
danger : OpenTofu reprend où l'état en est.

Il passe par OpenTofu, jamais par `oci compute instance launch` ni par
l'interface web : une instance créée hors de l'état serait invisible pour
`tofu`, qui en recréerait une deuxième. L'interface web n'aide pas non plus la
capacité — elle appelle la même API et reçoit la même erreur.

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

**Pas Pay As You Go.** C'est le levier qui débloque la capacité le plus souvent,
et il est écarté quand même : sur un compte non converti une ressource payante
échoue au lieu de facturer, et cette garantie est le seul coupe-circuit qui
existe — les budgets d'OCI ne font que notifier, ils ne coupent rien. La
conversion est IRRÉVERSIBLE. Voir `docs/couts.md`.
