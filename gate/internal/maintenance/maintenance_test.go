package maintenance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"golang.org/x/text/language"
)

func TestLangueRepli(t *testing.T) {
	cas := []struct {
		locale string
		veut   string
	}{
		{"fr", "fr"},
		{"fr_CA", "fr"}, // forme Minecraft, normalisée en fr-CA par Gate
		{"fr-CA", "fr"},
		{"en_US", "en"},
		{"es-MX", "es"},
		{"pt-BR", "pt"}, // le cas qui casse un découpage naïf de chaîne
		{"pt-PT", "pt"},
		{"th-TH", "th"},
		{"th", "th"},
		{"de-DE", "en"}, // langue connue du monde, inconnue de la table
		{"ja", "en"},
		{"", "en"},         // locale absente
		{"zzz", "en"},      // locale absurde
		{"und", "en"},      // indéterminée
		{"nan-Hant", "en"}, // sous-tag de base à trois lettres
	}
	for _, c := range cas {
		tag := language.Make(strings.ReplaceAll(c.locale, "_", "-"))
		if got := langue(tag); got != c.veut {
			t.Errorf("langue(%q) = %q, attendu %q", c.locale, got, c.veut)
		}
	}
}

// Le test qui protège l'ajout d'une 6e langue : toute clé utilisée par le
// code doit exister dans CHAQUE langue, y compris chaque raison définie.
func TestTableComplète(t *testing.T) {
	clés := []string{"rappel", "deconnexion", "annulation",
		string(RaisonConteneur), string(RaisonHôte), string(raisonInconnue)}

	for lg, m := range messages {
		for _, k := range clés {
			if strings.TrimSpace(m[k]) == "" {
				t.Errorf("langue %q : clé %q manquante", lg, k)
			}
		}
		if len(m) != len(clés) {
			t.Errorf("langue %q : %d entrées, attendu %d (clé en trop ?)", lg, len(m), len(clés))
		}
	}
	if _, ok := messages[langueDéfaut]; !ok {
		t.Fatalf("la langue de repli %q est absente de la table", langueDéfaut)
	}
}

// Les gabarits doivent porter le bon nombre de verbes de format, sinon le
// joueur reçoit un « %!s(MISSING) » à la place de la raison.
func TestGabaritsFormatés(t *testing.T) {
	for lg := range messages {
		if n := strings.Count(texte(lg, "rappel"), "%s"); n != 2 {
			t.Errorf("langue %q : « rappel » a %d %%s, attendu 2", lg, n)
		}
		if n := strings.Count(texte(lg, "deconnexion"), "%s"); n != 2 {
			t.Errorf("langue %q : « deconnexion » a %d %%s, attendu 2", lg, n)
		}
		msg := fmt.Sprintf(texte(lg, "rappel"), durée(2*time.Minute), raisonTexte(lg, RaisonHôte))
		if strings.Contains(msg, "%!") {
			t.Errorf("langue %q : formatage cassé : %q", lg, msg)
		}
	}
}

// Une raison hors table ne doit JAMAIS afficher la clé brute au joueur.
func TestRaisonInconnue(t *testing.T) {
	if got := raisonTexte("fr", Raison("raison.inventée")); got != texte("fr", string(raisonInconnue)) {
		t.Errorf("raison inconnue rendue %q", got)
	}
	if got := raisonTexte("th", RaisonConteneur); got != messages["th"][string(RaisonConteneur)] {
		t.Errorf("raison connue mal rendue en th : %q", got)
	}
}

func TestPaliers(t *testing.T) {
	cas := []struct {
		restant time.Duration
		veut    []time.Duration
	}{
		{6 * time.Minute, []time.Duration{5 * time.Minute, 2 * time.Minute, time.Minute}},
		{5 * time.Minute, []time.Duration{5 * time.Minute, 2 * time.Minute, time.Minute}},
		{4 * time.Minute, []time.Duration{2 * time.Minute, time.Minute}}, // fenêtre raccourcie
		{90 * time.Second, []time.Duration{time.Minute}},
		{30 * time.Second, nil}, // trop tard pour prévenir
		{0, nil},
		{-time.Minute, nil},
	}
	for _, c := range cas {
		got := paliers(Warnings, c.restant)
		if len(got) != len(c.veut) {
			t.Fatalf("paliers(%v) = %v, attendu %v", c.restant, got, c.veut)
		}
		for i := range got {
			if got[i] != c.veut[i] {
				t.Errorf("paliers(%v)[%d] = %v, attendu %v", c.restant, i, got[i], c.veut[i])
			}
		}
	}

	// Ordre décroissant garanti même si Warnings est mal rangé, et zéro ignoré.
	got := paliers([]time.Duration{time.Minute, 0, 5 * time.Minute, 2 * time.Minute}, time.Hour)
	veut := []time.Duration{5 * time.Minute, 2 * time.Minute, time.Minute}
	for i := range veut {
		if i >= len(got) || got[i] != veut[i] {
			t.Fatalf("paliers non triés : %v", got)
		}
	}
}

func TestMinutesArrondiAuDessus(t *testing.T) {
	cas := []struct {
		d    time.Duration
		veut int
	}{
		{0, 0}, {-time.Second, 0}, {time.Second, 1}, {59 * time.Second, 1},
		{time.Minute, 1}, {61 * time.Second, 2}, {5 * time.Minute, 5},
	}
	for _, c := range cas {
		if got := minutes(c.d); got != c.veut {
			t.Errorf("minutes(%v) = %d, attendu %d", c.d, got, c.veut)
		}
	}
}

func TestPréavis(t *testing.T) {
	if got := préavis(); got != 5*time.Minute {
		t.Errorf("préavis() = %v, attendu 5m", got)
	}
}

func TestDormirAnnulé(t *testing.T) {
	ctx, annuler := context.WithCancel(context.Background())
	annuler()
	if dormir(ctx, time.Hour) {
		t.Error("dormir devrait rendre false sur un contexte annulé")
	}
	if dormir(ctx, 0) {
		t.Error("dormir(0) devrait rendre false sur un contexte annulé")
	}
	if !dormir(context.Background(), 0) {
		t.Error("dormir(0) devrait rendre true sans annulation")
	}
}

// Le témoin est le seul lien entre Gate et l'hôte : si poser/retirer se
// désynchronise, l'hôte met à jour pendant que des joueurs sont connectés, ou
// n'y va jamais. Retirer doit être idempotent — c'est appelé au démarrage,
// quand le fichier n'existe presque jamais.
func TestTémoinPoséPuisRetiré(t *testing.T) {
	ÉtatPath = filepath.Join(t.TempDir(), "etat")
	pl := &plugin{log: logr.Discard()}

	pl.retirerTémoin() // absent : ne doit ni paniquer ni journaliser une erreur

	pl.poserTémoin(Window{StartsAt: time.Now(), Raison: RaisonHôte})
	contenu, err := os.ReadFile(ÉtatPath)
	if err != nil {
		t.Fatalf("témoin non posé : %v", err)
	}
	if !strings.HasPrefix(string(contenu), string(RaisonHôte)) {
		t.Errorf("le témoin doit nommer la raison, il contient %q", contenu)
	}

	pl.retirerTémoin()
	if _, err := os.Stat(ÉtatPath); !os.IsNotExist(err) {
		t.Errorf("témoin encore là après retrait : %v", err)
	}
}
