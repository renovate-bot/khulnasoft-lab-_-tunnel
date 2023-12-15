package image

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"golang.org/x/xerrors"

	"github.com/aquasecurity/trivy/pkg/fanal/image/token"
	"github.com/aquasecurity/trivy/pkg/fanal/types"
	"github.com/aquasecurity/trivy/pkg/log"
)

func tryRemote(ctx context.Context, imageName string, ref name.Reference, option types.DockerOption) (types.Image, error) {
	var remoteOpts []remote.Option
	if option.InsecureSkipTLSVerify {
		t := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		remoteOpts = append(remoteOpts, remote.WithTransport(t))
	}

	if option.Platform != "" {
		s, err := parsePlatform(ref, option.Platform)
		if err != nil {
			return nil, err
		}
		remoteOpts = append(remoteOpts, remote.WithPlatform(*s))
	}

	domain := ref.Context().RegistryStr()
	auth := token.GetToken(ctx, domain, option)

	if auth.Username != "" && auth.Password != "" {
		remoteOpts = append(remoteOpts, remote.WithAuth(&auth))
	} else if option.RegistryToken != "" {
		bearer := authn.Bearer{Token: option.RegistryToken}
		remoteOpts = append(remoteOpts, remote.WithAuth(&bearer))
	} else {
		remoteOpts = append(remoteOpts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	desc, err := remote.Get(ref, remoteOpts...)
	if err != nil {
		return nil, err
	}

	img, err := desc.Image()
	if err != nil {
		return nil, err
	}

	// Return v1.Image if the image is found in Docker Registry
	return remoteImage{
		name:       imageName,
		Image:      img,
		ref:        implicitReference{ref: ref},
		descriptor: desc,
	}, nil

}

func parsePlatform(ref name.Reference, p string) (*v1.Platform, error) {
	// OS wildcard, implicitly pick up the first os found in the image list.
	// e.g. */amd64
	if strings.HasPrefix(p, "*/") {
		index, err := remote.Index(ref)
		if err != nil {
			// Not a multi-arch image
			if _, ok := err.(*remote.ErrSchema1); ok {
				log.Logger.Debug("Ignored --platform as the image is not multi-arch")
				return nil, nil
			}
			return nil, xerrors.Errorf("remote index error: %w", err)
		}
		m, err := index.IndexManifest()
		if err != nil {
			return nil, xerrors.Errorf("remote index manifest error: %w", err)
		}
		if len(m.Manifests) == 0 {
			log.Logger.Debug("Ignored --platform as the image is not multi-arch")
			return nil, nil
		}
		if m.Manifests[0].Platform != nil {
			// Replace with the detected OS
			// e.g. */amd64 => linux/amd64
			p = m.Manifests[0].Platform.OS + strings.TrimPrefix(p, "*")
		}
	}
	platform, err := v1.ParsePlatform(p)
	if err != nil {
		return nil, xerrors.Errorf("platform parse error: %w", err)
	}
	return platform, nil
}

type remoteImage struct {
	name       string
	ref        implicitReference
	descriptor *remote.Descriptor
	v1.Image
}

func (img remoteImage) Name() string {
	return img.name
}

func (img remoteImage) ID() (string, error) {
	return ID(img)
}

func (img remoteImage) LayerIDs() ([]string, error) {
	return LayerIDs(img)
}

func (img remoteImage) RepoTags() []string {
	tag := img.ref.TagName()
	if tag == "" {
		return []string{}
	}
	return []string{fmt.Sprintf("%s:%s", img.ref.RepositoryName(), tag)}
}

func (img remoteImage) RepoDigests() []string {
	repoDigest := fmt.Sprintf("%s@%s", img.ref.RepositoryName(), img.descriptor.Digest.String())
	return []string{repoDigest}
}

type implicitReference struct {
	ref name.Reference
}

func (r implicitReference) TagName() string {
	if t, ok := r.ref.(name.Tag); ok {
		return t.TagStr()
	}
	return ""
}

func (r implicitReference) RepositoryName() string {
	ctx := r.ref.Context()
	reg := ctx.RegistryStr()
	repo := ctx.RepositoryStr()

	// Default registry
	if reg != name.DefaultRegistry {
		return fmt.Sprintf("%s/%s", reg, repo)
	}

	// Trim default namespace
	// See https://docs.docker.com/docker-hub/official_repos
	return strings.TrimPrefix(repo, "library/")
}
