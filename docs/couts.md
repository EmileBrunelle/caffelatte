# Rester à 0 $

Le projet existe pour apprendre OCI sans payer. Ce document est la liste de ce
qui peut faire sortir de l'argent, parce que c'est la seule partie où une erreur
coûte autre chose que du temps.

## La décision qui compte : Always Free pur, ou Pay As You Go

**Compte Always Free jamais converti** — impossible d'être facturé. Une ressource
payante ne se crée tout simplement pas. Le prix de cette garantie : la capacité
ARM à Montréal est intermittente et les comptes gratuits passent après les
autres. Elle peut ne jamais venir.

**Compte converti en Pay As You Go** — les ressources Always Free restent
gratuites, et la capacité ARM devient nettement plus accessible. Mais le filet
disparaît : une ressource payante se crée et se facture au lieu d'échouer.

**Décision : rester Always Free, jamais convertir.** Le compte est personnel et
personne ne surveille une facture quotidienne. La conversion est IRRÉVERSIBLE et
n'est jamais automatique — c'est un geste délibéré, donc un geste qu'on s'interdit.
Attendre la capacité ARM coûte du temps ; la perdre de vue en Pay As You Go coûte
de l'argent réel. Le runbook de capacité applique cette décision.

Le budget et les alertes de `terraform/main.tf` ne sont donc qu'une ceinture de
sécurité pour un cas qui ne devrait jamais arriver : ils NOTIFIENT, ils ne
coupent rien. Aucun garde-fou de facturation n'existe chez Oracle.
L'alerte sur la *prévision* est celle qui sert : elle avertit avant que l'argent
sorte, pas après.

## Ce qui facture, et qu'on oublie

| Piège | Détail |
|---|---|
| Volumes de démarrage | 200 Go gratuits au TOTAL pour le tenancy, volumes de démarrage compris. Trois instances à 100 Go dépassent. |
| Instance arrêtée | Le volume de démarrage continue de compter dans les 200 Go. Arrêter n'est pas supprimer. |
| IP publique réservée | Gratuite tant qu'attachée. Détachée et gardée, elle facture. |
| Stockage objet | ~20 Go gratuits. Les versions et les téléversements multiparts inachevés comptent. Mettre une règle de cycle de vie. |
| Sortie réseau | 10 To/mois. Confortable, mais un dépôt restic mal configuré qui réécrit tout chaque nuit y arrive. |
| Forme non gratuite | `VM.Standard.E4.Flex` ressemble à `E2.1.Micro` dans la liste. Seule `E2.1.Micro` et `A1.Flex` sont gratuites. |

## Le point de comparaison honnête

Servarica (Montréal) coûte quelques dollars par mois pour plus de RAM garantie,
sans jeu de capacité et sans risque de facture surprise. **Si l'objectif était
juste d'héberger un serveur Minecraft, ce serait le meilleur choix, et de loin.**

OCI se justifie ici pour une seule raison : apprendre une plateforme
infonuagique — compartiments, politiques IAM, VCN et Security Lists, Bastion,
budgets. Ces concepts se transposent vers AWS et Azure ; un VPS n'en enseigne
aucun. Tant que c'est bien ça l'objectif, le jeu de capacité fait partie du prix.
