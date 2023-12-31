package licensing

import (
	"fmt"
	"io"
	"sync"

	classifier "github.com/google/licenseclassifier/v2"
	"github.com/google/licenseclassifier/v2/assets"
	"golang.org/x/xerrors"

	"github.com/aquasecurity/trivy/pkg/fanal/types"
	"github.com/aquasecurity/trivy/pkg/log"
)

var cf *classifier.Classifier
var classifierOnce sync.Once

func initGoogleClassifier() error {
	// Initialize the default classifier once.
	// This loading is expensive and should be called only when the license classification is needed.
	var err error
	classifierOnce.Do(func() {
		log.Logger.Debug("Loading the the default license classifier...")
		cf, err = assets.DefaultClassifier()
	})
	return err
}

// Classify detects and classifies the license found in a file
func Classify(filePath string, r io.Reader) (*types.LicenseFile, error) {
	content, err := io.ReadAll(r)
	if err != nil {
		return nil, xerrors.Errorf("unable to read a license file %q: %w", filePath, err)
	}
	if err = initGoogleClassifier(); err != nil {
		return nil, err
	}

	var findings []types.LicenseFinding
	var matchType types.LicenseType
	seen := map[string]struct{}{}

	// Use 'github.com/google/licenseclassifier' to find licenses
	result := cf.Match(cf.Normalize(content))

	for _, match := range result.Matches {
		if match.Confidence <= 0.9 {
			continue
		}
		if _, ok := seen[match.Name]; ok {
			continue
		}

		seen[match.Name] = struct{}{}

		switch match.MatchType {
		case "Header":
			matchType = types.LicenseTypeHeader
		case "License":
			matchType = types.LicenseTypeFile
		}
		licenseLink := fmt.Sprintf("https://spdx.org/licenses/%s.html", match.Name)

		findings = append(findings, types.LicenseFinding{
			Name:       match.Name,
			Confidence: match.Confidence,
			Link:       licenseLink,
		})
	}
	return &types.LicenseFile{
		Type:     matchType,
		FilePath: filePath,
		Findings: findings,
	}, nil
}
