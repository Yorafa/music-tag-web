package utils

import (
	"strings"
	"testing"
)

// The handler tests exercise these rules through real files, but their
// fixture names contain no characters SanitizePath rewrites — so the
// sanitising step had no test of its own and a mutation removing it
// survived. It matters more here than it looks: a tag containing "/" is
// not hypothetical ("AC/DC", "Godspeed You! Black Emperor / F#A#∞"),
// and an unsanitised expansion turns a rename into a path escape or a
// name the shell cannot hold.

func fullVars() map[string]string {
	return map[string]string{
		"title":       "晴天",
		"artist":      "周杰伦",
		"album":       "叶惠美",
		"albumartist": "周杰伦",
		"genre":       "流行",
		"year":        "2003",
		"tracknumber": "3",
		"discnumber":  "1",
	}
}

func TestExpandFilenameTemplate(t *testing.T) {
	t.Run("renders every field the dialog offers", func(t *testing.T) {
		for _, f := range RenameTemplateFields {
			got, err := ExpandFilenameTemplate("${"+f+"}", fullVars())
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			if got.Name == "" {
				t.Errorf("%s expanded to nothing", f)
			}
		}
	})

	t.Run("year is a real value, not the literal placeholder", func(t *testing.T) {
		// The regression: `year` was never a variable, and the
		// permissive renderer left the text in the output, so a file
		// ended up called "artist - ${year} - title.mp3".
		got, err := ExpandFilenameTemplate(
			"${artist} - ${year} - ${title}", fullVars())
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "周杰伦 - 2003 - 晴天" {
			t.Errorf("Name = %q", got.Name)
		}
		if strings.Contains(got.Name, "$") {
			t.Errorf("Name still carries a placeholder: %q", got.Name)
		}
	})

	t.Run("an unknown field is an error, never text in the name", func(t *testing.T) {
		got, err := ExpandFilenameTemplate("${artist} - ${album_artist}", fullVars())
		if err == nil {
			t.Fatal("expected an error for an unknown field")
		}
		if len(got.Unknown) != 1 || got.Unknown[0] != "album_artist" {
			t.Errorf("Unknown = %v", got.Unknown)
		}
		// The message has to name the field AND the alternatives, or the
		// operator is left guessing at the allow-list.
		if !strings.Contains(err.Error(), "album_artist") ||
			!strings.Contains(err.Error(), "albumartist") {
			t.Errorf("unhelpful error: %v", err)
		}
	})

	t.Run("sanitises characters a filename cannot hold", func(t *testing.T) {
		vars := fullVars()
		vars["artist"] = `AC/DC`
		vars["album"] = `Godspeed You! Black Emperor: F#A#∞`
		got, err := ExpandFilenameTemplate("${artist} - ${album}", vars)
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(got.Name, `/\:*?"<>|`) {
			t.Errorf("Name still holds a path or shell metacharacter: %q", got.Name)
		}
	})

	t.Run("an empty field renders empty and is reported", func(t *testing.T) {
		// The chosen semantics: the gap stays visible in the name and
		// the caller is told, rather than the template quietly
		// collapsing to something the operator did not ask for.
		vars := fullVars()
		vars["genre"] = "" // the file has no genre tag
		got, err := ExpandFilenameTemplate(
			"${artist} - ${genre} - ${title}", vars)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "周杰伦 -  - 晴天" {
			t.Errorf("Name = %q, want the separators kept", got.Name)
		}
		if len(got.Empty) != 1 || got.Empty[0] != "genre" {
			t.Errorf("Empty = %v, want [genre]", got.Empty)
		}
	})

	t.Run("a field absent from the map is reported like an empty one", func(t *testing.T) {
		// Missing and empty are different causes but the same visible
		// outcome, and the caller renders the name either way.
		got, err := ExpandFilenameTemplate("${genre}", map[string]string{"title": "T"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "" {
			t.Errorf("Name = %q", got.Name)
		}
		if len(got.Empty) != 1 || got.Empty[0] != "genre" {
			t.Errorf("Empty = %v, want [genre]", got.Empty)
		}
	})

	t.Run("reports every empty field, not just the first", func(t *testing.T) {
		got, _ := ExpandFilenameTemplate("${artist} - ${album} - ${year}", map[string]string{})
		if len(got.Empty) != 3 {
			t.Errorf("Empty = %v, want 3 entries", got.Empty)
		}
	})

	t.Run("a repeated field is reported once per occurrence", func(t *testing.T) {
		got, _ := ExpandFilenameTemplate("${title} - ${title}", map[string]string{})
		if len(got.Empty) != 2 {
			t.Errorf("Empty = %v", got.Empty)
		}
	})

	t.Run("an empty template expands to nothing without erroring", func(t *testing.T) {
		// TemplateFieldError is what rejects the empty case for the
		// endpoints; the expander itself stays total.
		got, err := ExpandFilenameTemplate("", fullVars())
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "" {
			t.Errorf("Name = %q", got.Name)
		}
	})
}

func TestTemplateFieldError(t *testing.T) {
	if err := TemplateFieldError("${artist}"); err != nil {
		t.Errorf("a valid template was rejected: %v", err)
	}
	if err := TemplateFieldError(""); err == nil {
		t.Error("an empty template was accepted")
	}
	if err := TemplateFieldError("   "); err == nil {
		t.Error("a blank template was accepted")
	}
	// The one that matters: no placeholder means every selected file
	// gets the same literal name.
	if err := TemplateFieldError("track01"); err == nil {
		t.Error("a template with no placeholders was accepted")
	}
}
