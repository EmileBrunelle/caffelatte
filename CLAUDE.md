# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

`caffelatte` : serveur Minecraft (Java + Bedrock) pour une dizaine d'amis, décrit
entièrement en code, hébergé sur le tier gratuit d'Oracle Cloud. Le dépôt est
**public** — aucune adresse IP, aucun nom d'hôte, aucun secret en clair.

**Tout est en français** : prose, commentaires, messages d'erreur, et jusqu'aux
identifiants Go (`entrée`, `résoudreMojang`, `errDéjàAutorisé`, accents inclus).
Écrire en anglais ici détonne. Les messages de commit sont en français **sans
accents** — suivre le style des commits existants.

## Commandes

```bash
# Go (depuis gate/) — Go 1.26
go build ./...
go test ./...
go test ./internal/invitation/ -run TestPlafond -v   # un seul test

# OpenTofu — TOUJOURS `tofu`, jamais `terraform`
cd terraform && tofu init -backend-config=backend.hcl
tofu plan     # tfvars pris automatiquement depuis terraform.tfvars
tofu fmt -check && tofu validate

# Ansible — depuis la racine
ansible-galaxy collection install -r requirements.yml   # requis avant lint
ansible-lint ansible/
ansible-playbook ansible/site.yml --syntax-check
```

**Le CI ne lance pas `go test`.** Il fait `ansible-lint`, un scan Trivy de
l'image Minecraft (échec sur CVE haute/critique corrigeable) et
`tofu fmt`/`validate`/`checkov`. Les tests Go se lancent donc à la main.

Le scan d'IaC est **Checkov, et pas tfsec ni `trivy config`** : ni l'un ni
l'autre n'a de règle Oracle Cloud, donc tous deux passent en silence quoi qu'il
y ait dans `terraform/`. Vérifié : sur une `oci_core_security_list` ouvrant le
port 22 à `0.0.0.0/0`, tfsec répond « No problems detected » avec `passed: 0`.
Les exemptions, chacune justifiée, vivent dans `.checkov.yaml`.

Les CVE que le projet ne peut pas corriger lui-même (netty embarqué par Mojang
dans `server.jar`, dépendances de `rcon-cli`) sont listées dans `.trivyignore`
**avec une date d'expiration** : passé cette date le CI redevient rouge, pour
qu'une exemption soit relue au lieu de devenir définitive.

En usage normal, **on ne lance pas Ansible depuis le portable** : la machine se
configure elle-même (voir plus bas). Le mode push
(`ansible-playbook -i ansible/inventory.yml ansible/site.yml`) existe pour le
dépannage et exige l'inventaire réel, non versionné.

## La chaîne de déploiement — le mécanisme central

`tofu apply` crée l'instance et y injecte `terraform/cloud-init.yaml` comme
`user_data`. Le cloud-init installe `git` + `ansible-core`, écrit
`/usr/local/sbin/caffelatte-pull` et arme un timer systemd horaire qui fait un
`ansible-pull` sur la branche **`deploy`**. La machine est donc son propre
contrôleur Ansible : après le premier boot, **tout déploiement est un
`git push` sur `deploy`**, jamais une connexion entrante.

Ce n'est pas un choix esthétique. Le port 22 est fermé hors du VCN par la
Security List et il n'y a pas de bastion (ADR 0010) ; le seul canal disponible
est du sortant sur 443. `main` et `deploy` ne sont pas synchronisés
automatiquement — pousser `main:deploy` est un geste de release délibéré.

Trois conséquences à ne jamais oublier :

- **`user_data` n'est lu qu'au TOUT PREMIER boot.** Modifier `cloud-init.yaml`
  n'a aucun effet sur une instance vivante ; il faudrait la détruire et la
  recréer — donc relâcher la capacité ARM, qui met des semaines à s'obtenir.
  Tout ce qui est modifiable après coup appartient à Ansible, pas au cloud-init.
- **Un mauvais commit sur `deploy` se déploie tout seul** dans l'heure. Marqué
  `ponytail:` dans `cloud-init.yaml` ; le correctif prévu est une validation de
  branche, pas le retrait du timer (c'est lui qui permet de réparer sans accès).
- `ansible/inventory-pull.yml` cible `localhost` avec `ansible_connection:
  local`, pas le vrai nom d'hôte : `ansible-pull` ne s'autorise que
  `<fqdn>,<hostname>,localhost,127.0.0.1`, et un hostname inattendu donnerait un
  « no hosts matched » qui **réussit** silencieusement sans rien faire.

## L'exécution sur l'hôte

Podman rootless sous l'utilisateur `svc`, piloté par Quadlet (unités
`.container`/`.network` en systemd --user). Deux réseaux, et la frontière entre
eux est structurante :

- `mc.network` est `Internal=true` — Minecraft et Geyser n'ont **aucune sortie
  Internet**. Minecraft ne publie aucun port sur l'hôte ; il n'est joignable que
  par Gate.
- Gate est le seul conteneur attaché aussi au réseau par défaut. C'est donc le
  seul à pouvoir joindre Mojang et GeyserMC — d'où le fait qu'il porte
  l'authentification en ligne et la liste blanche à la place de celle de
  vanilla (`WHITELIST=false` côté backend, qui échouerait sans egress).

Le monde vit sur un volume bloc OCI séparé monté sur `/srv/minecraft`, pas sur
le volume de démarrage, pour survivre à la perte de l'instance. Les secrets
passent par `podman secret` (tmpfs), jamais par variable d'environnement.

## `gate/` — l'arbre d'invitations

Le cœur non évident. Une liste JSON sur disque, **clé = UUID authentifié,
jamais le pseudo** (`TestAutorisationParUUID`). Chaque entrée garde son parrain,
mais `Parrain` est une **trace historique, pas une clé étrangère** : retirer
quelqu'un de `CAFFELATTE_PARRAINS` n'éjecte jamais ses invités déjà inscrits.

Le pont Bedrock est le piège principal. `/invite-bedrock` résout le gamertag en
XUID via GeyserMC, puis calcule l'UUID avec `floodgate.BedrockData.JavaUuid()` —
le même UUID v5 déterministe que Gate posera au login. `FloodgateJavaUuid()`,
dans le même paquet, est un **faux ami** : identité différente, invité refusé
malgré son invitation.

Autres invariants que les tests gèlent, à ne pas « simplifier » :

- Hook sur `LoginEvent`, pas `PreLoginEvent` : au PreLogin le profil Floodgate
  n'a pas encore été réécrit, l'UUID final n'existe pas.
- `/invite` est une commande proxy, pas un mot-clé de chat : intercepter le chat
  casse la signature de message obligatoire depuis 1.19.1 (déconnexion immédiate).
- **Échec fermé partout** : API injoignable ou réponse ambiguë ⇒ rien n'est
  inscrit. GeyserMC répond **503** (pas 404) pour un gamertag inconnu, donc
  indistinguable d'une panne, et traité pareil.
- Fichier JSON corrompu ⇒ le proxy refuse de démarrer, jamais une liste vide
  silencieuse (`TestPersistanceFichierCorrompu`) — sinon tous les joueurs
  légitimes sont éjectés sans que personne comprenne. Écriture atomique
  (temp + fsync + rename).
- Les cycles et les orphelins dans l'arbre sont affichés à part, jamais avalés.
- Validation non assainissante aux frontières : `pseudoValide` est sans `(?m)`,
  donc `$` refuse bien `"toto\nop"`. Ne pas « moderniser » cette regex.

## Pièges d'infrastructure

- **Deux couches de pare-feu indépendantes** : la Security List OCI (Terraform)
  et firewalld (rôle `base`). Ouvrir d'un seul côté donne un timeout silencieux.
  C'est la perte de temps numéro un sur ce projet.
- **`minecraft_sleep_when_empty` doit rester `false`.** Oracle récupère les
  instances dont CPU **et** réseau **et** mémoire sont sous ~20 % sur 7 jours ;
  un serveur qui dort vide les trois axes. C'est aussi pourquoi l'agent de
  monitoring est activé — sans lui, seul le CPU est publié.
- **Jamais une deuxième instance A1**, même minuscule : dépasser le quota d'une
  tenancy gratuite fait désactiver puis supprimer **toutes** les A1. Les limites
  affichées par `oci limits value list` sont plus hautes parce que le compte est
  en essai à crédits — rien n'empêche techniquement de dépasser.
- `minecraft_parrains: []` fait échouer le démarrage **exprès** : pas de parrain
  = personne ne peut inviter, mieux vaut refuser de démarrer qu'ouvrir le
  serveur sans le savoir.
- `is_ipv6enabled` s'active à la création du VCN et jamais après. Recréer le VCN
  emporterait le sous-réseau et l'instance, donc la capacité ARM.
- La capacité ARM manque en permanence : `scripts/attendre-capacite-arm.sh`
  boucle sur `CreateComputeCapacityReport` (lecture seule, aucun verrou d'état)
  et ne réveille `tofu` qu'en cas de capacité. Voir
  `docs/runbook-capacite-arm.md` — ne pas remettre `tofu` dans la boucle.

## Fichiers attendus, non versionnés

`terraform/terraform.tfvars`, `terraform/backend.hcl` (contient la Customer
Secret Key), `ansible/inventory.yml`, `ansible/group_vars/*/secrets.yml` (SOPS),
`*.agekey`, `floodgate.pem`. Les `.example.yml` versionnés montrent la forme.

`.sops.yaml` contient encore le placeholder `age1REMPLACE_PAR_TA_CLE_PUBLIQUE` :
le chiffrement des secrets n'est pas opérationnel en l'état.

## Conventions

Un commentaire `ponytail:` marque une simplification **délibérée** avec son
plafond connu et son chemin de sortie. Ce n'est pas de la dette à corriger au
passage — la lire avant de « réparer » ce qu'elle décrit.

`docs/adr.md` porte les décisions et leurs raisons. Une décision d'architecture
prise ici s'y ajoute plutôt que de vivre dans un message de commit.
