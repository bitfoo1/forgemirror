// Package registry copies container images and other OCI artifacts between
// registries, tag by tag, without changing their digests.
package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"go.bitfoo.de/forgemirror/internal/config"
)

const userAgent = "forgemirror"

// Repository is an image repository on a registry with the credentials to
// reach it.
type Repository struct {
	// Host is the registry host[:port].
	Host string
	// Path is the repository path below the host, e.g. "acme/app".
	Path string
	// Insecure selects plain HTTP.
	Insecure bool
	Username string
	Password config.Secret
}

// String returns host/path; it carries no credentials.
func (r Repository) String() string { return r.Host + "/" + r.Path }

func (r Repository) tag(tag string) (name.Tag, error) {
	var opts []name.Option
	if r.Insecure {
		opts = append(opts, name.Insecure)
	}

	t, err := name.NewTag(r.String()+":"+tag, opts...)
	if err != nil {
		return name.Tag{}, fmt.Errorf("image reference %s:%s: %w", r, tag, err)
	}

	return t, nil
}

func (r Repository) auth() authn.Authenticator {
	if r.Password.Reveal() == "" {
		return authn.Anonymous
	}

	return &authn.Basic{Username: r.Username, Password: r.Password.Reveal()}
}

// Client talks to registries. Each call authenticates per repository, so a
// source and a target on different registries never see each other's
// credentials. The library's own transport is used rather than the forge API
// client: registry bearer tokens are scoped to their registry there, and
// blob downloads may legitimately redirect to object storage on another host.
type Client struct{}

// New returns a [Client].
func New() *Client { return &Client{} }

func options(ctx context.Context, r Repository) []remote.Option {
	return []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuth(r.auth()),
		remote.WithUserAgent(userAgent),
	}
}

// Digests returns the manifest digest of each tag that exists in r. A tag or
// repository that does not exist is left out. With deniedIsMissing, an
// access denial counts as missing too: some registries (ghcr.io) deny a pull
// of a package that does not exist yet instead of reporting it unknown, and
// a real permission problem then shows up when the copy is attempted.
func (c *Client) Digests(
	ctx context.Context,
	r Repository,
	tags []string,
	deniedIsMissing bool,
) (map[string]string, error) {
	out := make(map[string]string, len(tags))

	for _, tag := range tags {
		ref, err := r.tag(tag)
		if err != nil {
			return nil, err
		}

		desc, err := remote.Head(ref, options(ctx, r)...)
		if err != nil {
			if missing(err, deniedIsMissing) {
				continue
			}

			return nil, fmt.Errorf("reading %s:%s: %w", r, tag, err)
		}
		out[tag] = desc.Digest.String()
	}

	return out, nil
}

func missing(err error, deniedIsMissing bool) bool {
	var terr *transport.Error
	if !errors.As(err, &terr) {
		return false
	}

	switch terr.StatusCode {
	case http.StatusNotFound:
		return true
	case http.StatusUnauthorized, http.StatusForbidden:
		return deniedIsMissing
	default:
		return false
	}
}

// Copy copies tag from src to dst. Manifests and indexes are written as read,
// so the digest stays the same and signatures that refer to it stay valid.
func (c *Client) Copy(ctx context.Context, src, dst Repository, tag string) error {
	srcRef, err := src.tag(tag)
	if err != nil {
		return err
	}
	dstRef, err := dst.tag(tag)
	if err != nil {
		return err
	}

	desc, err := remote.Get(srcRef, options(ctx, src)...)
	if err != nil {
		return fmt.Errorf("reading %s:%s: %w", src, tag, err)
	}

	if desc.MediaType.IsIndex() {
		idx, idxErr := desc.ImageIndex()
		if idxErr != nil {
			return fmt.Errorf("reading index %s:%s: %w", src, tag, idxErr)
		}
		if err = remote.WriteIndex(dstRef, idx, options(ctx, dst)...); err != nil {
			return fmt.Errorf("writing %s:%s: %w", dst, tag, err)
		}

		return nil
	}

	img, err := desc.Image()
	if err != nil {
		return fmt.Errorf("reading image %s:%s: %w", src, tag, err)
	}
	if err = remote.Write(dstRef, img, options(ctx, dst)...); err != nil {
		return fmt.Errorf("writing %s:%s: %w", dst, tag, err)
	}

	return nil
}
