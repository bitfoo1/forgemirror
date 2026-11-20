package forge

import (
	"errors"
	"fmt"
	"strings"
	"text/template"

	"go.bitfoo.de/forgemirror/internal/repo"
)

// Mirror markers tell visitors of a target repository that it is a copy and
// that changes belong on the source. They are on by default, because a
// mirror that looks like the original invites issues and pull requests
// nobody reads.
const (
	DefaultMirrorNote  = "Mirror, synced by ForgeMirror. Do not push here."
	DefaultMirrorTopic = "forgemirror"
)

// mirrorNoteSeparator joins the source description and the note.
const mirrorNoteSeparator = " · "

var errMultiLineNote = errors.New("must render to a single line")

// Marker is the resolved mirror marker of one repository on one target. An
// empty field is disabled.
type Marker struct {
	Note             string
	Topic            string
	URL              string
	DescriptionLimit int
}

// ResolveMarker applies the layers in order (last wins) on top of the
// defaults. An empty string in a layer disables that part of the marker.
func ResolveMarker(layers ...Settings) Marker {
	m := Marker{Note: DefaultMirrorNote, Topic: DefaultMirrorTopic}
	for _, l := range layers {
		if l.MirrorNote != nil {
			m.Note = *l.MirrorNote
		}
		if l.MirrorTopic != nil {
			m.Topic = *l.MirrorTopic
		}
		if l.MirrorURL != nil {
			m.URL = *l.MirrorURL
		}
		if l.MirrorDescriptionLimit != nil {
			m.DescriptionLimit = *l.MirrorDescriptionLimit
		}
	}

	return m
}

// Apply adds the marker to r. The note is a text/template rendered with src;
// it is appended to the description rather than replacing it, so the
// mirror keeps saying what the project is.
func (m Marker) Apply(r Repo, src repo.Repository) (Repo, error) {
	note, err := renderMirrorNote(m.Note, src)
	if err != nil {
		return r, err
	}
	if m.URL != "" {
		publicURL, urlErr := renderMirrorURL(m.URL, src)
		if urlErr != nil {
			return r, urlErr
		}
		r.Website = publicURL
		if publicURL != "" {
			note = appendNote(note, "Contribute: "+publicURL)
		}
	}
	limit := m.DescriptionLimit
	if limit == 0 {
		limit = defaultMirrorDescriptionLimit
	}
	if r.Description, err = fitMirrorDescription(r.Description, note, limit); err != nil {
		return r, err
	}

	if m.Topic != "" {
		r.Topics = normalizeTopics(append(append([]string(nil), r.Topics...), m.Topic))
	}

	return r, nil
}

// appendNote is idempotent, so a description that already carries the note
// (e.g. set verbatim in settings.description) does not get it twice.
func appendNote(description, note string) string {
	description = strings.TrimSpace(description)
	switch {
	case note == "", strings.HasSuffix(description, note):
		return description
	case description == "":
		return note
	default:
		return description + mirrorNoteSeparator + note
	}
}

// validateMirrorNote checks that the note parses and renders. Unknown fields
// would otherwise only fail at sync time, on every run.
func validateMirrorNote(text string) error {
	_, err := renderMirrorNote(text, repo.Repository{Owner: "owner", Name: "name", FullName: "owner/name"})

	return err
}

func renderMirrorNote(text string, src repo.Repository) (string, error) {
	tmpl, err := template.New("mirror_note").Option("missingkey=error").Parse(text)
	if err != nil {
		return "", fmt.Errorf("mirror_note: %w", err)
	}

	var b strings.Builder
	if execErr := tmpl.Execute(&b, src); execErr != nil {
		return "", fmt.Errorf("mirror_note: %w", execErr)
	}

	// Forges store descriptions as one line; a newline would be stripped by
	// the forge and the note reported as changed on every run.
	note := strings.TrimSpace(b.String())
	if strings.ContainsAny(note, "\r\n") {
		return "", fmt.Errorf("mirror_note: %w", errMultiLineNote)
	}

	return note, nil
}
