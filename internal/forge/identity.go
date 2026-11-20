package forge

import (
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"go.bitfoo.de/forgemirror/internal/repo"
)

const defaultMirrorDescriptionLimit = 350

func validateMirrorIdentity(settings Settings) error {
	const maxLimit = 4096
	if settings.MirrorDescriptionLimit != nil &&
		(*settings.MirrorDescriptionLimit < 1 || *settings.MirrorDescriptionLimit > maxLimit) {
		return errors.New("mirror_description_limit: must be between 1 and 4096 bytes")
	}
	if settings.MirrorURL != nil {
		_, err := renderMirrorURL(
			*settings.MirrorURL,
			repo.Repository{Owner: "owner", Name: "name", FullName: "owner/name"},
		)
		return err
	}
	return nil
}

func renderMirrorURL(text string, source repo.Repository) (string, error) {
	rendered, err := renderMirrorNote(text, source)
	if err != nil {
		return "", errors.New("mirror_url: invalid URL template")
	}
	if rendered == "" {
		return "", nil
	}
	u, err := url.Parse(rendered)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return "", errors.New("mirror_url: must render an HTTP(S) URL without credentials")
	}
	return rendered, nil
}

func fitMirrorDescription(description, note string, limit int) (string, error) {
	if note == "" {
		return clipDescription(description, limit), nil
	}
	combined := appendNote(description, note)
	if len(combined) <= limit {
		return combined, nil
	}
	if len(note) > limit {
		return "", errors.New("mirror marker exceeds mirror_description_limit; shorten it or increase the limit")
	}
	suffix := mirrorNoteSeparator + note
	if len(suffix) >= limit {
		return note, nil
	}
	prefix := strings.TrimSuffix(combined, suffix)
	prefix = strings.TrimSpace(clipDescription(prefix, limit-len(suffix)))
	return appendNote(prefix, note), nil
}

func clipDescription(text string, limit int) string {
	end := min(len(text), max(limit, 0))
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	return text[:end]
}
