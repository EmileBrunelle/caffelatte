#!/bin/sh
set -eu
# Le serveur est immuable ; /data ne porte que l'état (monde, bans, ops).
[ -f /data/eula.txt ] || echo "eula=true" > /data/eula.txt
[ -f /data/server.jar ] || ln -s /opt/mc/server.jar /data/server.jar
# Fabric ne scanne que <cwd>/mods (ici /data) : les mods sont du contenu
# d'image, pas de l'état, donc un lien symbolique plutôt qu'une copie dans le volume.
[ -e /data/mods ] || ln -s /opt/mc/mods /data/mods

# server.properties est régénéré à chaque démarrage : la config vit dans l'image
# et les variables d'env, pas dans un fichier que quelqu'un a édité à la main en 2024.
cat > /data/server.properties <<PROPS
server-port=25565
online-mode=${ONLINE_MODE:-false}
enable-rcon=true
rcon.port=25575
rcon.password=$(cat /run/secrets/rcon_password)
motd=${MOTD:-caffelatte}
view-distance=${VIEW_DISTANCE:-10}
simulation-distance=${SIMULATION_DISTANCE:-8}
max-players=${MAX_PLAYERS:-20}
# white-list=false EST VOULU. NE PAS « corriger ».
# Le contrôle d'accès est fait par Gate (gate/internal/invitation), pas ici.
# Raison : le réseau podman « mc » est Internal=true, ce conteneur n'a AUCUN
# accès sortant. Or « whitelist add <pseudo> » en vanilla résout le profil
# auprès de l'API Mojang : un pseudo absent du cache local échoue toujours.
# Garder la liste blanche vanilla active rejetterait donc tous les invités.
# Gate, lui, a l'egress, fait déjà l'authentification Mojang et connaît le vrai
# UUID ; et comme ce serveur n'est joignable QUE par Gate (réseau interne,
# aucun port publié), filtrer dans le proxy est un point de contrôle suffisant.
white-list=${WHITELIST:-false}
enforce-whitelist=${WHITELIST:-false}
PROPS

# online-mode=false est SÛR uniquement derrière Gate : FabricProxy-Lite valide
# un secret partagé, et le port 25565 n'est joignable que par le réseau interne
# du pod (jamais exposé sur l'hôte). Sans les deux, n'importe qui usurpe un pseudo.
mkdir -p /data/config
cat > /data/config/FabricProxy-Lite.toml <<FPL
hackOnlineMode = true
hackEarlySend = false
hackMessageChain = true
secret = "$(cat /run/secrets/forwarding_secret)"
FPL

exec java -Xms"${HEAP:-4G}" -Xmx"${HEAP:-4G}" \
  -XX:+UseG1GC -XX:MaxGCPauseMillis=130 -XX:+ParallelRefProcEnabled \
  -jar /opt/mc/fabric-server-launch.jar nogui
