package invitation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.minekube.com/gate/pkg/edition/bedrock/geyser/floodgate"
	"go.minekube.com/gate/pkg/util/uuid"
)

func TestPseudoValide(t *testing.T) {
	ok := []string{"Notch", "abc", "_____", "Toto_123", "ABCDEFGHIJKLMNOP"}
	for _, s := range ok {
		if !pseudoValide.MatchString(s) {
			t.Errorf("pseudo légitime refusé : %q", s)
		}
	}

	// Chaque entrée refusée ici est une injection dans l'URL de l'API Mojang ou
	// dans la liste d'autorisation si elle passe.
	ko := []string{
		"",                  // vide
		"ab",                // trop court
		"ABCDEFGHIJKLMNOPQ", // 17, trop long
		"Toto Titi",         // espace
		"Toto;op Toto",      // séparateur de commande shell
		"Toto\nop Toto",     // saut de ligne : seconde requête HTTP
		"Toto\rop Toto",     // retour chariot
		"Toto\top",          // tabulation
		"\nop Toto",         // saut de ligne en tête
		"Toto\n",            // saut de ligne en queue (Go : $ = fin de texte)
		"\"Toto\"",          // guillemets
		"'Toto'",            // apostrophes
		"$(id)",             // substitution shell
		"`id`",              // substitution shell
		"Toto|op",           // pipe
		"Toto&op",           // enchaînement
		"../../etc/passwd",  // traversée
		"Émile",             // unicode
		"Тoto",              // homoglyphe cyrillique
		"Toto\u0000",        // octet nul
		"Toto​",             // espace de largeur nulle
	}
	for _, s := range ko {
		if pseudoValide.MatchString(s) {
			t.Errorf("pseudo dangereux accepté : %q", s)
		}
	}
}

func TestAutorisationParUUID(t *testing.T) {
	parrain := mustUUID(t, "069a79f4-44e9-4726-a5be-fca90e38aaf5")
	autre := mustUUID(t, "853c80ef-3c37-49fd-aa49-938b674adae6")
	cfg := &config{parrains: map[uuid.UUID]bool{parrain: true}, plafond: 2}

	if !cfg.parrains[parrain] {
		t.Error("le parrain configuré devrait être autorisé")
	}
	if cfg.parrains[autre] {
		t.Error("un UUID inconnu ne doit jamais être autorisé")
	}
	if cfg.parrains[uuid.Nil] {
		t.Error("l'UUID nul (source non joueur) ne doit jamais être autorisé")
	}
}

// TestRefusALaConnexion couvre la décision prise dans auLogin. Les champs de
// proxy.LoginEvent ne sont pas exportés, on teste donc autorisé() directement :
// c'est là qu'est toute la logique, auLogin ne fait qu'y brancher Deny().
func TestRefusALaConnexion(t *testing.T) {
	parrain := mustUUID(t, "069a79f4-44e9-4726-a5be-fca90e38aaf5")
	invité := mustUUID(t, "853c80ef-3c37-49fd-aa49-938b674adae6")
	inconnu := mustUUID(t, "3c2f3d0e-9f8b-4a1c-8f3d-0e9f8b4a1c8f")

	p := plugineTest(t, parrain)
	if err := p.liste.ajouter(entrée{UUID: invité, Pseudo: "Ami", Parrain: parrain}, p.cfg.plafond); err != nil {
		t.Fatalf("ajout : %v", err)
	}

	if !p.autorisé(parrain) {
		t.Error("un parrain doit pouvoir se connecter sans figurer dans la liste")
	}
	if !p.autorisé(invité) {
		t.Error("un invité listé doit pouvoir se connecter")
	}
	if p.autorisé(inconnu) {
		t.Error("un UUID absent de la liste doit être refusé")
	}
	if p.autorisé(uuid.Nil) {
		t.Error("l'UUID nul doit être refusé")
	}
}

func TestPlafond(t *testing.T) {
	parrain := mustUUID(t, "069a79f4-44e9-4726-a5be-fca90e38aaf5")
	autre := mustUUID(t, "853c80ef-3c37-49fd-aa49-938b674adae6")
	p := plugineTest(t, parrain) // plafond 2

	if err := p.liste.ajouter(entrée{UUID: idTest(1), Pseudo: "Un", Parrain: parrain}, p.cfg.plafond); err != nil {
		t.Fatalf("1re invitation : %v", err)
	}
	if err := p.liste.ajouter(entrée{UUID: idTest(2), Pseudo: "Deux", Parrain: parrain}, p.cfg.plafond); err != nil {
		t.Fatalf("2e invitation : %v", err)
	}
	if err := p.liste.ajouter(entrée{UUID: idTest(3), Pseudo: "Trois", Parrain: parrain}, p.cfg.plafond); !errors.Is(err, errPlafond) {
		t.Errorf("la 3e doit buter sur le plafond, obtenu : %v", err)
	}
	// Le plafond est par parrain.
	if err := p.liste.ajouter(entrée{UUID: idTest(4), Pseudo: "Quatre", Parrain: autre}, p.cfg.plafond); err != nil {
		t.Errorf("le plafond d'un parrain ne doit pas bloquer un autre parrain : %v", err)
	}
	// Réinviter quelqu'un de déjà listé ne consomme pas de place et ne le
	// duplique pas.
	if err := p.liste.ajouter(entrée{UUID: idTest(1), Pseudo: "Un", Parrain: parrain}, p.cfg.plafond); !errors.Is(err, errDéjàAutorisé) {
		t.Errorf("un doublon doit être signalé comme déjà autorisé, obtenu : %v", err)
	}

	// Le plafond survit au redémarrage : il se déduit du fichier, pas d'un
	// compteur en mémoire.
	rechargée, err := chargerListe(p.cfg.chemin)
	if err != nil {
		t.Fatalf("rechargement : %v", err)
	}
	if err := rechargée.ajouter(entrée{UUID: idTest(5), Pseudo: "Cinq", Parrain: parrain}, p.cfg.plafond); !errors.Is(err, errPlafond) {
		t.Errorf("le plafond doit tenir après redémarrage, obtenu : %v", err)
	}
}

func TestPersistanceAllerRetour(t *testing.T) {
	dir := t.TempDir()
	chemin := filepath.Join(dir, "invitations.json")
	parrain := mustUUID(t, "069a79f4-44e9-4726-a5be-fca90e38aaf5")

	l, err := chargerListe(chemin)
	if err != nil {
		t.Fatalf("fichier absent : %v", err)
	}
	if l.contient(idTest(1)) {
		t.Error("une liste neuve ne doit contenir personne")
	}

	e := entrée{UUID: idTest(1), Pseudo: "Ami", Parrain: parrain, Date: time.Now().UTC().Truncate(time.Second)}
	if err := l.ajouter(e, 5); err != nil {
		t.Fatalf("ajout : %v", err)
	}

	rechargée, err := chargerListe(chemin)
	if err != nil {
		t.Fatalf("rechargement : %v", err)
	}
	if !rechargée.contient(idTest(1)) {
		t.Fatal("l'invité doit survivre au redémarrage")
	}
	got := rechargée.autorisés[idTest(1)]
	if got.Pseudo != "Ami" || got.Parrain != parrain || !got.Date.Equal(e.Date) {
		t.Errorf("entrée dénaturée par l'aller-retour : %+v", got)
	}

	// Écriture atomique : aucun fichier temporaire ne doit traîner.
	noms, _ := filepath.Glob(filepath.Join(dir, ".invitations-*"))
	if len(noms) != 0 {
		t.Errorf("fichiers temporaires laissés derrière : %v", noms)
	}
	// Le fichier n'est lisible que par Gate.
	fi, err := os.Stat(chemin)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("permissions %v, attendu 0600", fi.Mode().Perm())
	}
}

func TestPersistanceFichierCorrompu(t *testing.T) {
	// Un fichier illisible ne doit JAMAIS se transformer en liste vide : ça
	// éjecterait tous les joueurs sans que personne ne comprenne pourquoi.
	for nom, contenu := range map[string]string{
		"JSON tronqué":   `{"autorises": [{"uuid":`,
		"pas du JSON":    "coucou",
		"entrée sans ID": `{"autorises": [{"pseudo":"Ami"}]}`,
		"UUID invalide":  `{"autorises": [{"uuid":"pas-un-uuid","pseudo":"Ami"}]}`,
	} {
		chemin := filepath.Join(t.TempDir(), "invitations.json")
		if err := os.WriteFile(chemin, []byte(contenu), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := chargerListe(chemin); err == nil {
			t.Errorf("%s : chargerListe doit échouer, pas renvoyer une liste vide", nom)
		}
	}
}

func TestRésoudreMojang(t *testing.T) {
	attendu := mustUUID(t, "069a79f4-44e9-4726-a5be-fca90e38aaf5")

	cas := []struct {
		nom       string
		statut    int
		corps     string
		veutErr   error
		veutUUID  uuid.UUID
		veutNom   string
		veutÉchec bool
	}{
		{nom: "profil trouvé", statut: 200,
			corps:    `{"id":"069a79f444e94726a5befca90e38aaf5","name":"Notch"}`,
			veutUUID: attendu, veutNom: "Notch"},
		{nom: "UUID avec tirets", statut: 200,
			corps:    `{"id":"069a79f4-44e9-4726-a5be-fca90e38aaf5","name":"Notch"}`,
			veutUUID: attendu, veutNom: "Notch"},
		{nom: "pseudo inexistant", statut: 404, corps: "", veutErr: errPseudoInconnu},
		{nom: "réponse vide (ancienne API)", statut: 204, corps: "", veutErr: errPseudoInconnu},
		{nom: "panne Mojang", statut: 500, corps: "", veutÉchec: true},
		{nom: "limitation de débit", statut: 429, corps: "", veutÉchec: true},
		{nom: "corps illisible", statut: 200, corps: "pas du json", veutÉchec: true},
		{nom: "UUID absent", statut: 200, corps: `{"name":"Notch"}`, veutÉchec: true},
		{nom: "UUID nul", statut: 200,
			corps: `{"id":"00000000-0000-0000-0000-000000000000","name":"Notch"}`, veutÉchec: true},
		// Le pseudo canonique vient de l'extérieur : il repasse la frontière de
		// confiance avant d'être écrit dans la liste et affiché en jeu.
		{nom: "pseudo canonique hostile", statut: 200,
			corps: `{"id":"069a79f444e94726a5befca90e38aaf5","name":"Not ch\n§4"}`, veutÉchec: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/Notch" {
					t.Errorf("chemin inattendu : %q", r.URL.Path)
				}
				w.WriteHeader(c.statut)
				_, _ = w.Write([]byte(c.corps))
			}))
			defer srv.Close()
			ancien := mojangProfilURL
			mojangProfilURL = srv.URL + "/"
			defer func() { mojangProfilURL = ancien }()

			id, nom, err := résoudreMojang(context.Background(), "Notch")
			switch {
			case c.veutErr != nil:
				if !errors.Is(err, c.veutErr) {
					t.Fatalf("attendu %v, obtenu %v", c.veutErr, err)
				}
			case c.veutÉchec:
				if err == nil {
					t.Fatalf("attendu un échec, obtenu %v / %q", id, nom)
				}
			default:
				if err != nil {
					t.Fatalf("erreur inattendue : %v", err)
				}
				if id != c.veutUUID || nom != c.veutNom {
					t.Fatalf("obtenu %v / %q, attendu %v / %q", id, nom, c.veutUUID, c.veutNom)
				}
			}
		})
	}
}

// TestRésoudreMojangInjoignable : échec fermé quand l'API ne répond pas.
func TestRésoudreMojangInjoignable(t *testing.T) {
	srv := httptest.NewServer(nil)
	url := srv.URL + "/"
	srv.Close() // plus personne n'écoute

	ancien := mojangProfilURL
	mojangProfilURL = url
	defer func() { mojangProfilURL = ancien }()

	if _, _, err := résoudreMojang(context.Background(), "Notch"); err == nil {
		t.Error("une API injoignable doit produire une erreur, jamais une autorisation")
	}
}

// TestFormatFichier fige la forme sur disque : c'est un fichier qu'un humain
// lit et édite en cas de pépin.
func TestFormatFichier(t *testing.T) {
	chemin := filepath.Join(t.TempDir(), "invitations.json")
	l, err := chargerListe(chemin)
	if err != nil {
		t.Fatal(err)
	}
	parrain := mustUUID(t, "069a79f4-44e9-4726-a5be-fca90e38aaf5")
	if err := l.ajouter(entrée{UUID: idTest(1), Pseudo: "Ami", Parrain: parrain, Date: time.Unix(0, 0).UTC()}, 5); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(chemin)
	if err != nil {
		t.Fatal(err)
	}
	var f fichier
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("le fichier écrit doit être du JSON valide : %v", err)
	}
	if len(f.Autorises) != 1 || f.Autorises[0].Pseudo != "Ami" {
		t.Errorf("contenu inattendu : %s", b)
	}
}

// plugineTest monte un plugin sur un fichier jetable, plafond 2.
func plugineTest(t *testing.T, parrain uuid.UUID) *plugin {
	t.Helper()
	chemin := filepath.Join(t.TempDir(), "invitations.json")
	l, err := chargerListe(chemin)
	if err != nil {
		t.Fatalf("chargerListe : %v", err)
	}
	return &plugin{
		cfg:   &config{parrains: map[uuid.UUID]bool{parrain: true}, plafond: 2, chemin: chemin},
		liste: l,
	}
}

// idTest fabrique des UUID distincts et lisibles.
func idTest(n byte) uuid.UUID {
	var id uuid.UUID
	id[15] = n
	return id
}

func mustUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("UUID de test invalide %q : %v", s, err)
	}
	return id
}

// --- Chemin Bedrock ---------------------------------------------------------

func TestGamertagValide(t *testing.T) {
	// Un gamertag Xbox n'est PAS un pseudo Java : espaces autorisés, longueur
	// différente, pas de souligné.
	ok := []string{"a", "Notch", "Nice Duck 5", "ABCDEFGHIJKLMNOP", "A B C D E F G H"}
	for _, s := range ok {
		if !gamertagValide(s) {
			t.Errorf("gamertag légitime refusé : %q", s)
		}
	}

	ko := []string{
		"",                  // vide
		"ABCDEFGHIJKLMNOPQ", // 17, au-delà de ce que l'API accepte
		" Notch",            // espace en tête
		"Notch ",            // espace en queue
		"Notch  Duck",       // espace doublé
		" ",                 // espace seul
		"Notch_Duck",        // souligné : règle Java, pas Xbox
		"Notch#1234",        // suffixe moderne : absent du gamertag affiché
		"Notch;op",          // séparateur de commande shell
		"Notch\nop",         // saut de ligne : seconde requête HTTP
		"Notch\rop",         // retour chariot
		"Notch\top",         // tabulation
		"Notch\n",           // saut de ligne en queue (Go : $ = fin de texte)
		"\nNotch",           // saut de ligne en tête
		"\"Notch\"",         // guillemets
		"$(id)",             // substitution shell
		"`id`",              // substitution shell
		"Notch|op",          // pipe
		"Notch&op",          // enchaînement
		"../../etc/passwd",  // traversée
		"Émile",             // unicode
		"Тotch",             // homoglyphe cyrillique
		"Notch\u0000",       // octet nul
		"Notch​Duck",        // espace de largeur nulle
		"Notch%20Duck",      // pré-encodage : on n'accepte que le vrai espace
	}
	for _, s := range ko {
		if gamertagValide(s) {
			t.Errorf("gamertag dangereux accepté : %q", s)
		}
	}
}

// TestUUIDFloodgate fige la correspondance XUID → UUID. C'est LE test qui
// garantit qu'un invité Bedrock entre : l'UUID inscrit doit être exactement
// celui que Gate posera sur son profil.
//
// Double vérification volontaire : contre des valeurs figées (une régression
// silencieuse de la formule casse le test) ET contre le code de Gate lui-même,
// (*floodgate.BedrockData).JavaUuid(), qui est ce que
// geyser.(*Integration).onGameProfile met dans profile.GameProfile.ID.
func TestUUIDFloodgate(t *testing.T) {
	cas := []struct {
		xuid int64
		veut string
	}{
		{2535453759792258, "5374e1e2-8742-5f31-bbb0-3d81730368af"}, // XUID réel de « Notch » (api.geysermc.org)
		{987654321, "b271944e-8e91-5188-abbb-047f6cbd03a7"},
		{1, "19377ab7-8146-5c0e-8eed-b770dec1d1e8"},
		{9223372036854775807, "23345245-d52e-5240-8834-3c06d6b09f64"},
	}
	for _, c := range cas {
		got, err := uuidFloodgate(c.xuid)
		if err != nil {
			t.Fatalf("XUID %d : %v", c.xuid, err)
		}
		if got.String() != c.veut {
			t.Errorf("XUID %d → %s, attendu %s", c.xuid, got, c.veut)
		}
		// Ce que Gate calculera à la connexion, par son propre code.
		gate, err := (&floodgate.BedrockData{Xuid: c.xuid}).JavaUuid()
		if err != nil {
			t.Fatalf("JavaUuid(%d) : %v", c.xuid, err)
		}
		if got != gate {
			t.Fatalf("DIVERGENCE avec Gate pour le XUID %d : liste %s, profil %s", c.xuid, got, gate)
		}
	}

	// Faux ami à ne jamais confondre : FloodgateJavaUuid() est l'identité
	// côté Bedrock d'un triplet Floodgate, pas le profil appliqué par Gate.
	autre := (&floodgate.BedrockData{Xuid: 987654321}).FloodgateJavaUuid()
	if id, _ := uuidFloodgate(987654321); id == autre {
		t.Error("uuidFloodgate ne doit pas renvoyer new UUID(0, xuid)")
	}
}

func TestRésoudreGeyser(t *testing.T) {
	attendu := mustUUID(t, "5374e1e2-8742-5f31-bbb0-3d81730368af")

	cas := []struct {
		nom       string
		statut    int
		corps     string
		veutErr   error
		veutUUID  uuid.UUID
		veutÉchec bool
	}{
		{nom: "gamertag trouvé", statut: 200, corps: `{"xuid":2535453759792258}`, veutUUID: attendu},
		// L'API répond 503 (pas 404) quand le gamertag est absent de son cache.
		{nom: "gamertag inexistant", statut: 503,
			corps:   `{"message":"Unable to find user in our cache."}`,
			veutErr: errGamertagInconnu},
		{nom: "gamertag refusé par l'API", statut: 400,
			corps: `{"message":"gamertag is empty or longer than 16 chars"}`, veutÉchec: true},
		{nom: "panne GeyserMC", statut: 500, corps: "", veutÉchec: true},
		{nom: "limitation de débit", statut: 429, corps: "", veutÉchec: true},
		{nom: "corps illisible", statut: 200, corps: "pas du json", veutÉchec: true},
		{nom: "XUID absent", statut: 200, corps: `{}`, veutÉchec: true},
		{nom: "XUID nul", statut: 200, corps: `{"xuid":0}`, veutÉchec: true},
		{nom: "XUID négatif", statut: 200, corps: `{"xuid":-1}`, veutÉchec: true},
		{nom: "XUID textuel hostile", statut: 200, corps: `{"xuid":"2535453759792258"}`, veutÉchec: true},
	}

	for _, c := range cas {
		t.Run(c.nom, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// L'espace du gamertag doit arriver percent-encodé, une seule
				// fois, et rien d'autre ne doit avoir bougé.
				if r.URL.EscapedPath() != "/Nice%20Duck" {
					t.Errorf("chemin inattendu : %q", r.URL.EscapedPath())
				}
				if r.URL.Path != "/Nice Duck" {
					t.Errorf("chemin décodé inattendu : %q", r.URL.Path)
				}
				w.WriteHeader(c.statut)
				_, _ = w.Write([]byte(c.corps))
			}))
			defer srv.Close()
			ancien := geyserXuidURL
			geyserXuidURL = srv.URL + "/"
			defer func() { geyserXuidURL = ancien }()

			id, nom, err := résoudreGeyser(context.Background(), "Nice Duck")
			switch {
			case c.veutErr != nil:
				if !errors.Is(err, c.veutErr) {
					t.Fatalf("attendu %v, obtenu %v", c.veutErr, err)
				}
			case c.veutÉchec:
				if err == nil {
					t.Fatalf("attendu un échec, obtenu %v / %q", id, nom)
				}
			default:
				if err != nil {
					t.Fatalf("erreur inattendue : %v", err)
				}
				if id != c.veutUUID || nom != "Nice Duck" {
					t.Fatalf("obtenu %v / %q, attendu %v / %q", id, nom, c.veutUUID, "Nice Duck")
				}
			}
		})
	}
}

// TestRésoudreGeyserCorpsHostile : un intermédiaire qui répond un flux infini
// ne doit ni noyer Gate ni le faire tourner indéfiniment.
func TestRésoudreGeyserCorpsHostile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		bloc := []byte(`{"xuid":1,"bourrage":"` + strings.Repeat("A", 4096) + `"`)
		for i := 0; i < 64; i++ {
			if _, err := w.Write(bloc); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()
	ancien := geyserXuidURL
	geyserXuidURL = srv.URL + "/"
	defer func() { geyserXuidURL = ancien }()

	fini := make(chan struct{})
	go func() {
		defer close(fini)
		if _, _, err := résoudreGeyser(context.Background(), "Nice Duck"); err == nil {
			t.Error("un corps surdimensionné doit produire une erreur, jamais une autorisation")
		}
	}()
	select {
	case <-fini:
	case <-time.After(10 * time.Second):
		t.Fatal("résoudreGeyser ne s'est pas arrêté sur un corps sans fin")
	}
}

// TestRésoudreGeyserTimeout : l'API qui ne répond jamais ne doit pas bloquer
// le parrain — échec fermé au bout du délai.
func TestRésoudreGeyserTimeout(t *testing.T) {
	bloque := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-bloque
	}))
	defer func() { close(bloque); srv.Close() }()
	ancien := geyserXuidURL
	geyserXuidURL = srv.URL + "/"
	defer func() { geyserXuidURL = ancien }()

	ctx, annuler := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer annuler()
	début := time.Now()
	if _, _, err := résoudreGeyser(ctx, "Nice Duck"); err == nil {
		t.Fatal("une API muette doit produire une erreur, jamais une autorisation")
	}
	if d := time.Since(début); d > 5*time.Second {
		t.Errorf("résoudreGeyser a attendu %v : le timeout ne borne rien", d)
	}
}

// TestRésoudreGeyserInjoignable : échec fermé quand l'API ne répond pas.
func TestRésoudreGeyserInjoignable(t *testing.T) {
	srv := httptest.NewServer(nil)
	url := srv.URL + "/"
	srv.Close() // plus personne n'écoute

	ancien := geyserXuidURL
	geyserXuidURL = url
	defer func() { geyserXuidURL = ancien }()

	if _, _, err := résoudreGeyser(context.Background(), "Nice Duck"); err == nil {
		t.Error("une API injoignable doit produire une erreur, jamais une autorisation")
	}
}

// --- Révocation -------------------------------------------------------------

func TestRévocation(t *testing.T) {
	parrain := mustUUID(t, "069a79f4-44e9-4726-a5be-fca90e38aaf5")
	autre := mustUUID(t, "853c80ef-3c37-49fd-aa49-938b674adae6")
	toujoursOK := func(entrée) bool { return true }

	monter := func(t *testing.T) *liste {
		t.Helper()
		l, err := chargerListe(filepath.Join(t.TempDir(), "invitations.json"))
		if err != nil {
			t.Fatal(err)
		}
		// Une entrée Java et une entrée Bedrock, même liste.
		java := entrée{UUID: idTest(1), Pseudo: "AmiJava", Parrain: parrain}
		bedrock := entrée{UUID: mustUUID(t, "5374e1e2-8742-5f31-bbb0-3d81730368af"),
			Pseudo: "Nice Duck", Parrain: autre}
		for _, e := range []entrée{java, bedrock} {
			if err := l.ajouter(e, 5); err != nil {
				t.Fatal(err)
			}
		}
		return l
	}

	t.Run("entrée Java par pseudo", func(t *testing.T) {
		l := monter(t)
		e, err := l.retirer("AmiJava", toujoursOK)
		if err != nil || e.UUID != idTest(1) {
			t.Fatalf("obtenu %v / %v", e, err)
		}
		if l.contient(idTest(1)) {
			t.Error("l'entrée doit avoir disparu de la liste")
		}
	})

	t.Run("entrée Bedrock par gamertag à espaces", func(t *testing.T) {
		l := monter(t)
		id := mustUUID(t, "5374e1e2-8742-5f31-bbb0-3d81730368af")
		if _, err := l.retirer("nice duck", toujoursOK); err != nil { // casse ignorée
			t.Fatalf("révocation Bedrock : %v", err)
		}
		if l.contient(id) {
			t.Error("l'entrée Bedrock doit avoir disparu")
		}
	})

	t.Run("par UUID", func(t *testing.T) {
		l := monter(t)
		if _, err := l.retirer(idTest(1).String(), toujoursOK); err != nil {
			t.Fatalf("révocation par UUID : %v", err)
		}
	})

	t.Run("cible absente", func(t *testing.T) {
		l := monter(t)
		if _, err := l.retirer("Personne", toujoursOK); !errors.Is(err, errAbsent) {
			t.Errorf("attendu errAbsent, obtenu %v", err)
		}
		if _, err := l.retirer(idTest(9).String(), toujoursOK); !errors.Is(err, errAbsent) {
			t.Errorf("attendu errAbsent pour un UUID inconnu, obtenu %v", err)
		}
	})

	t.Run("homonymes : refus plutôt que suppression au hasard", func(t *testing.T) {
		l := monter(t)
		if err := l.ajouter(entrée{UUID: idTest(7), Pseudo: "AmiJava", Parrain: autre}, 5); err != nil {
			t.Fatal(err)
		}
		if _, err := l.retirer("AmiJava", toujoursOK); !errors.Is(err, errAmbigu) {
			t.Errorf("attendu errAmbigu, obtenu %v", err)
		}
		if !l.contient(idTest(1)) || !l.contient(idTest(7)) {
			t.Error("un refus pour ambiguïté ne doit rien supprimer")
		}
	})

	t.Run("non autorisé : rien n'est supprimé", func(t *testing.T) {
		l := monter(t)
		// « autre » n'est pas le parrain de l'entrée Java.
		_, err := l.retirer("AmiJava", func(e entrée) bool { return e.Parrain == autre })
		if !errors.Is(err, errPasLeParrain) {
			t.Fatalf("attendu errPasLeParrain, obtenu %v", err)
		}
		if !l.contient(idTest(1)) {
			t.Error("une révocation refusée ne doit rien supprimer")
		}
		rechargée, err := chargerListe(l.chemin)
		if err != nil {
			t.Fatal(err)
		}
		if !rechargée.contient(idTest(1)) {
			t.Error("une révocation refusée ne doit pas non plus toucher le disque")
		}
	})

	t.Run("le retrait survit au redémarrage et rend sa place au plafond", func(t *testing.T) {
		l := monter(t)
		if _, err := l.retirer("AmiJava", toujoursOK); err != nil {
			t.Fatal(err)
		}
		rechargée, err := chargerListe(l.chemin)
		if err != nil {
			t.Fatal(err)
		}
		if rechargée.contient(idTest(1)) {
			t.Error("le retrait doit être persisté")
		}
		if err := rechargée.ajouter(entrée{UUID: idTest(8), Pseudo: "Neuf", Parrain: parrain}, 1); err != nil {
			t.Errorf("la place libérée doit être réutilisable : %v", err)
		}
	})
}

// TestRévocationAutorisation couvre le modèle de décision de révoquer() :
// parrain = ses propres invités, admin = n'importe qui.
func TestRévocationAutorisation(t *testing.T) {
	parrain := mustUUID(t, "069a79f4-44e9-4726-a5be-fca90e38aaf5")
	autre := mustUUID(t, "853c80ef-3c37-49fd-aa49-938b674adae6")
	admin := mustUUID(t, "3c2f3d0e-9f8b-4a1c-8f3d-0e9f8b4a1c8f")

	cas := []struct {
		nom     string
		auteur  uuid.UUID
		parrain uuid.UUID // parrain de l'entrée visée
		veut    bool
	}{
		{"son propre invité", parrain, parrain, true},
		{"l'invité d'un autre", parrain, autre, false},
		{"admin sur l'invité d'un autre", admin, autre, true},
		{"admin sur un invité orphelin", admin, uuid.Nil, true},
		{"un inconnu qui n'est ni parrain ni admin", autre, parrain, false},
		{"parrain sur un invité orphelin", parrain, uuid.Nil, false},
	}
	p := plugineTest(t, parrain)
	p.cfg.admins = map[uuid.UUID]bool{admin: true}
	for _, c := range cas {
		got := p.peutRévoquer(c.auteur, entrée{UUID: idTest(1), Parrain: c.parrain})
		if got != c.veut {
			t.Errorf("%s : obtenu %v, attendu %v", c.nom, got, c.veut)
		}
	}
	// La console (UUID nul) ne révoque rien, même une entrée orpheline.
	if p.peutRévoquer(uuid.Nil, entrée{UUID: idTest(1)}) {
		t.Error("l'UUID nul ne doit donner aucun droit de révocation")
	}
}

// --- Arbre des invitations ---------------------------------------------------

func jour(n int) time.Time {
	return time.Date(2024, 1, n, 0, 0, 0, 0, time.UTC)
}

// compterLignesEntrées compte les lignes d'entrée (indentées, format
// « nom (date) ») dans un rendu d'arbreInvitations, sans compter les lignes
// d'en-tête de section (qui contiennent aussi des parenthèses).
func compterLignesEntrées(rendu string) int {
	n := 0
	for _, ligne := range strings.Split(rendu, "\n") {
		if strings.HasPrefix(ligne, "  ") {
			n++
		}
	}
	return n
}

func TestArbreInvitationsMultiNiveaux(t *testing.T) {
	config := idTest(1) // parrain de configuration, jamais dans autorisés
	alice := idTest(2)
	bob := idTest(3)
	carla := idTest(4)

	m := map[uuid.UUID]entrée{
		alice: {UUID: alice, Pseudo: "Alice", Parrain: config, Date: jour(1)},
		bob:   {UUID: bob, Pseudo: "Bob", Parrain: alice, Date: jour(2)},
		carla: {UUID: carla, Pseudo: "Carla", Parrain: alice, Date: jour(3)},
	}

	got := arbreInvitations(m)
	veut := "Parrain absent de la liste (invités directement, ou par quelqu'un qui n'y est plus) :\n" +
		"  Alice (2024-01-01)\n" +
		"    Bob (2024-01-02)\n" +
		"    Carla (2024-01-03)"
	if got != veut {
		t.Errorf("arbre inattendu :\n%s\n--- attendu ---\n%s", got, veut)
	}
}

func TestArbreInvitationsOrphelin(t *testing.T) {
	config := idTest(1)
	parrainRetiré := idTest(2)
	alice := idTest(3)
	orphelin := idTest(4)

	m := map[uuid.UUID]entrée{
		alice:    {UUID: alice, Pseudo: "Alice", Parrain: config, Date: jour(1)},
		orphelin: {UUID: orphelin, Pseudo: "Orphelin", Parrain: parrainRetiré, Date: jour(2)},
	}

	got := arbreInvitations(m)

	if !strings.Contains(got, "Parrain absent de la liste") {
		t.Errorf("section des orphelins manquante :\n%s", got)
	}
	if !strings.Contains(got, "Orphelin (2024-01-02)") {
		t.Errorf("l'orphelin n'apparaît pas :\n%s", got)
	}
	// Le total des lignes affichées doit correspondre au nombre d'entrées :
	// un orphelin ne doit ni disparaître, ni être compté deux fois.
	if n := compterLignesEntrées(got); n != len(m) {
		t.Errorf("total affiché = %d, attendu %d :\n%s", n, len(m), got)
	}
}

func TestArbreInvitationsCycle(t *testing.T) {
	a := idTest(1)
	b := idTest(2)

	// a et b se pointent l'un l'autre : impossible en usage normal (le
	// parrain doit déjà être dans la liste au moment de l'invitation), mais
	// le JSON peut être édité à la main.
	m := map[uuid.UUID]entrée{
		a: {UUID: a, Pseudo: "A", Parrain: b, Date: jour(1)},
		b: {UUID: b, Pseudo: "B", Parrain: a, Date: jour(2)},
	}

	done := make(chan string, 1)
	go func() { done <- arbreInvitations(m) }()
	select {
	case got := <-done:
		if !strings.Contains(got, "Parrainage circulaire détecté") {
			t.Errorf("section de cycle manquante :\n%s", got)
		}
		if n := compterLignesEntrées(got); n != len(m) {
			t.Errorf("total affiché = %d, attendu %d :\n%s", n, len(m), got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("arbreInvitations a bouclé sur un cycle au lieu de terminer")
	}
}

func TestArbreInvitationsPropreParrain(t *testing.T) {
	soi := idTest(1)
	m := map[uuid.UUID]entrée{
		soi: {UUID: soi, Pseudo: "Soi", Parrain: soi, Date: jour(1)},
	}

	done := make(chan string, 1)
	go func() { done <- arbreInvitations(m) }()
	select {
	case got := <-done:
		if !strings.Contains(got, "Soi (2024-01-01)") {
			t.Errorf("l'entrée son-propre-parrain n'apparaît pas :\n%s", got)
		}
		if n := compterLignesEntrées(got); n != 1 {
			t.Errorf("total affiché = %d, attendu 1 :\n%s", n, got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("arbreInvitations a bouclé sur une entrée son-propre-parrain")
	}
}

func TestArbreInvitationsTriDéterministe(t *testing.T) {
	config := idTest(1)
	m := map[uuid.UUID]entrée{}
	for i := byte(2); i < 20; i++ {
		id := idTest(i)
		m[id] = entrée{UUID: id, Pseudo: fmt.Sprintf("J%02d", i), Parrain: config, Date: jour(int(i))}
	}

	référence := arbreInvitations(m)
	for i := 0; i < 20; i++ {
		if got := arbreInvitations(m); got != référence {
			t.Fatalf("sortie non déterministe à l'itération %d :\n%s\n--- vs ---\n%s", i, got, référence)
		}
	}
}
