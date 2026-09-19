package invitation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"

	"go.minekube.com/gate/pkg/edition/bedrock/geyser/floodgate"
	"go.minekube.com/gate/pkg/util/uuid"
)

// gamertagValide est la frontière de confiance du chemin Bedrock. Un gamertag
// Xbox n'obéit PAS aux règles d'un pseudo Java : il peut contenir des espaces,
// et l'API GeyserMC le borne elle-même à 16 caractères (elle répond
// « gamertag is empty or longer than 16 chars » au-delà — vérifié le
// 2026-09-19 sur api.geysermc.org/v2/xbox/xuid/).
//
// Règle retenue : 1 à 16 caractères, lettres ASCII et chiffres, séparés par
// des espaces simples — jamais d'espace en tête, en queue, ni doublé (c'est la
// forme imposée par la grammaire ci-dessous, pas un nettoyage a posteriori).
// Le suffixe « #1234 » des gamertags modernes est refusé : il n'apparaît pas
// dans le gamertag affiché que le cache GeyserMC indexe. On refuse, on
// n'assainit pas : le gamertag part dans un chemin d'URL et finit dans un
// fichier JSON lu par un humain.
var gamertagMotif = regexp.MustCompile(`^[A-Za-z0-9]+( [A-Za-z0-9]+)*$`)

// gamertagValide applique le motif ET la longueur. Le motif est ASCII pur,
// len() compte donc bien des caractères.
func gamertagValide(g string) bool {
	return len(g) >= 1 && len(g) <= 16 && gamertagMotif.MatchString(g)
}

// geyserXuidURL est une variable pour que le test pointe sur un serveur
// httptest : aucun appel réseau en test. Chemin vérifié le 2026-09-19 :
// GET https://api.geysermc.org/v2/xbox/xuid/<gamertag> → {"xuid":2535453759792258}
// C'est le même service (api.geysermc.org/v2) que Gate consulte déjà lui-même
// pour les comptes liés et les skins — voir
// pkg/edition/bedrock/geyser/profile.go, GEYSER_API_URL.
var geyserXuidURL = "https://api.geysermc.org/v2/xbox/xuid/"

var errGamertagInconnu = errors.New("gamertag inconnu du cache GeyserMC")

// uuidFloodgate donne l'UUID que Gate posera sur ce joueur Bedrock à sa
// connexion. C'EST LE POINT CRITIQUE de tout le chemin Bedrock : inscrire un
// autre UUID que celui-là revient à refuser l'invité malgré son invitation.
//
// On n'invente rien, on appelle le code de Gate lui-même :
// pkg/edition/bedrock/geyser/floodgate/floodgate.go, (*BedrockData).JavaUuid()
// — un UUID v5 déterministe sur sha1("FloodgateXUID:" + xuid décimal). C'est
// exactement la valeur que pkg/edition/bedrock/geyser/geyser.go,
// (*Integration).onGameProfile() place dans profile.GameProfile.ID.
//
// Attention au faux ami : le même paquet expose FloodgateJavaUuid()
// (new UUID(0, xuid)), qui est l'identité côté Bedrock d'un triplet Floodgate
// et n'est PAS le profil appliqué par Gate.
//
// Réserve : si l'opérateur active bedrock.backendFloodgate.enabled (faux par
// défaut, pkg/edition/bedrock/config/config.go), onGameProfile peut remplacer
// cet UUID par celui du compte Java lié. Dans ce cas l'invité Bedrock doit
// être invité par son pseudo Java avec /invite.
func uuidFloodgate(xuid int64) (uuid.UUID, error) {
	return (&floodgate.BedrockData{Xuid: xuid}).JavaUuid()
}

// résoudreGeyser traduit un gamertag Xbox en UUID Floodgate via l'API
// GeyserMC. Gate est le seul conteneur avec un accès sortant, c'est donc lui
// qui peut le faire.
//
// Échec fermé : tout ce qui n'est pas un 200 avec un XUID strictement positif
// est une erreur, et rien n'est inscrit. L'API répond 503 (et non 404) quand
// le gamertag est absent de son cache ; on ne peut donc pas distinguer
// proprement « ce gamertag n'existe pas » d'une panne, et les deux mènent de
// toute façon au même refus.
func résoudreGeyser(ctx context.Context, gamertag string) (uuid.UUID, string, error) {
	ctx, annuler := context.WithTimeout(ctx, 5*time.Second)
	defer annuler()

	// gamertag a déjà passé gamertagValide : [A-Za-z0-9 ]. PathEscape encode
	// l'espace en %20 ; rien d'autre n'a besoin de l'être.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, geyserXuidURL+url.PathEscape(gamertag), nil)
	if err != nil {
		return uuid.Nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return uuid.Nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusServiceUnavailable:
		return uuid.Nil, "", errGamertagInconnu
	default:
		return uuid.Nil, "", fmt.Errorf("API GeyserMC : statut %d", resp.StatusCode)
	}

	var rép struct {
		XUID int64 `json:"xuid"`
	}
	// Corps borné : la réponse fait ~30 octets. On ne se laisse pas noyer par
	// un intermédiaire qui répondrait un flux infini.
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&rép); err != nil {
		return uuid.Nil, "", fmt.Errorf("réponse GeyserMC illisible : %w", err)
	}
	if rép.XUID <= 0 {
		// Floodgate refuse lui aussi le XUID 0 (floodgate.go, parse).
		return uuid.Nil, "", fmt.Errorf("XUID GeyserMC invalide : %d", rép.XUID)
	}

	id, err := uuidFloodgate(rép.XUID)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, "", fmt.Errorf("UUID Floodgate incalculable pour le XUID reçu : %w", err)
	}
	// Le gamertag canonique n'est pas demandé à l'API : il faudrait un second
	// aller-retour (/v2/xbox/gamertag/<xuid>) pour une simple étiquette. On
	// garde la saisie, déjà validée strictement.
	return id, gamertag, nil
}
