// Strict template rendering for filenames built from tags.
//
// # Why this exists next to RenderTemplate
//
// `RenderTemplate` is the permissive version: a key it does not find in
// the map is left in the output as the literal `${key}`, on the theory
// that showing the placeholder is more useful than showing nothing. That
// is fine for a display string and catastrophic for a filename. The
// rename path feeds its output straight to SanitizePath and then
// os.Rename, so a user who wrote the perfectly reasonable
// `${artist} - ${year} - ${title}` got a file literally named
//
//	周杰伦 - ${year} - 晴天.mp3
//
// and no error anywhere. `year` was not a bug in the user's template — it
// was simply never a template variable, and the renderer was swallowing
// that fact.
//
// So the strict version has two jobs:
//
//  1. Every placeholder resolves. An unknown key is an error the caller
//     can report, never text that reaches the filesystem.
//  2. It says which placeholders resolved to nothing. A tag that is
//     empty renders empty, and the surrounding separators stay —
//     `${artist} - ${genre} - ${title}` on a track with no genre is
//     "Artist -  - Title". That is deliberate (the operator sees the
//     gap in the preview and decides), but they can only see it if the
//     renderer tells them, so it does.
//
// The permissive renderer is untouched: it has other callers whose
// behaviour is not a filename.

package utils

import (
	"fmt"
	"sort"
	"strings"
)

// RenameTemplateFields are the keys a filename template may use, in the
// order the dialog lists them.
//
// It is an allow-list, not a suggestion. `ExpandFilenameTemplate`
// rejects anything else, which turns a typo (`${album_artist}`) into an
// error the operator sees rather than a file named after the typo.
//
// `filename` is deliberately absent. It means "the file's current name",
// and a template that reads it is asking to append a name to a name —
// `$(filename)` was presumably meant to be `${filename}` and the missing
// `{}` is invisible in a rendered result. Leaving it out means the
// dialog's chips and this list cannot drift apart.
var RenameTemplateFields = []string{
	"title", "artist", "album", "albumartist",
	"genre", "year", "tracknumber", "discnumber",
}

var renameFieldSet = func() map[string]bool {
	m := make(map[string]bool, len(RenameTemplateFields))
	for _, n := range RenameTemplateFields {
		m[n] = true
	}
	return m
}()

// Expansion is the result of rendering one template against one file.
type Expansion struct {
	// Name is the rendered name, WITHOUT the extension. Callers append
	// the file's own extension so a rename never changes the format.
	Name string

	// Empty lists the placeholders that resolved to no value, in the
	// order they appear in the template. The rendered name still has
	// their separators in it, so a caller that cares shows this.
	Empty []string

	// Unknown lists placeholders that are not template variables at
	// all. A non-empty Unknown is an error condition: the caller must
	// not write the result anywhere.
	Unknown []string
}

// ExpandFilenameTemplate renders `tmpl` against `vars` and refuses to
// produce a name containing an unresolved placeholder.
//
// The returned Expansion is usable even when Unknown is non-empty — the
// caller needs the Unknown list to report, and building it is the only
// way to find out. It is the caller's job to check before writing.
func ExpandFilenameTemplate(tmpl string, vars map[string]string) (Expansion, error) {
	out := Expansion{}
	unknown := map[string]bool{}

	// A missing key and a present-but-empty key are different facts and
	// are reported differently: the first is a template the operator
	// cannot have meant, the second is a file with nothing in that tag.
	// RenderTemplate cannot tell them apart, which is most of why this
	// function exists.
	rendered := templatePattern.ReplaceAllStringFunc(tmpl, func(match string) string {
		key := strings.TrimSpace(match[2 : len(match)-1])
		if !renameFieldSet[key] {
			unknown[key] = true
			return ""
		}
		v, ok := vars[key]
		if !ok || v == "" {
			out.Empty = append(out.Empty, key)
		}
		return v
	})

	for k := range unknown {
		out.Unknown = append(out.Unknown, k)
	}
	sort.Strings(out.Unknown)

	if len(out.Unknown) > 0 {
		return out, fmt.Errorf(
			"模板里有未知的字段 %s；可用字段：%s",
			strings.Join(out.Unknown, " / "),
			strings.Join(RenameTemplateFields, " / "))
	}

	out.Name = SanitizePath(strings.TrimSpace(rendered))
	return out, nil
}

// TemplateFieldError describes an unusable template in the words the
// dialog shows, for the case where the template is empty or has no
// placeholders at all (in which case every file would be renamed to the
// same literal string, and the batch collision check would catch it N
// times over instead of once).
func TemplateFieldError(tmpl string) error {
	if strings.TrimSpace(tmpl) == "" {
		return fmt.Errorf("模板为空")
	}
	if !strings.Contains(tmpl, "${") {
		return fmt.Errorf("模板里没有 ${字段} 占位符；照字面重命名会把所有文件改成同一个名字")
	}
	return nil
}
