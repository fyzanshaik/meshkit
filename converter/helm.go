package converter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/layer5io/meshkit/models/patterns"
	"github.com/pkg/errors"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"sigs.k8s.io/yaml"
)

type HelmConverter struct{}

func (h *HelmConverter) Convert(patternFile string) (string, error) {
	pattern, err := patterns.GetPatternFormat(patternFile)
	if err != nil {
		return "", errors.Wrap(err, "failed to load pattern file: "+patternFile)
	}

	patterns.ProcessAnnotations(pattern)

	k8sConverter := K8sConverter{}
	k8sManifest, err := k8sConverter.Convert(patternFile)
	if err != nil {
		return "", errors.Wrap(err, "failed to convert to k8s manifest")
	}

	fmt.Printf("K8s manifest generated, size: %d bytes\n", len(k8sManifest))

	chartName := sanitizeHelmName(pattern.Name)
	if chartName == "" {
		chartName = pattern.Name
	}

	chartVersion := pattern.Version

	chartContent, err := createHelmChartContent(k8sManifest, chartName, chartVersion)
	if err != nil {
		return "", errors.Wrap(err, "failed to create helm chart content")
	}

	return chartContent, nil
}

func createHelmChartContent(manifestContent, chartName, chartVersion string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", errors.Wrap(err, "failed to get user home directory")
	}

	mesheryDir := filepath.Join(homeDir, ".meshery")
	packageDir := filepath.Join(mesheryDir, "helm-packages")
	tempDir := filepath.Join(mesheryDir, "tmp", "helm")

	if err := os.MkdirAll(packageDir, 0755); err != nil {
		return "", errors.Wrap(err, "failed to create package directory")
	}

	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", errors.Wrap(err, "failed to create temp directory")
	}

	buildID := uuid.New().String()
	buildDir := filepath.Join(tempDir, buildID)
	chartSourcePath := filepath.Join(buildDir, chartName)

	defer func() {
		err := os.RemoveAll(buildDir)
		if err != nil {
			fmt.Printf("Warning: Failed to clean up build directory: %+v\n", errors.Wrap(err, "failed to remove build directory"))
		}
	}()
	if err := os.MkdirAll(chartSourcePath, 0755); err != nil {
		return "", errors.Wrap(err, "failed to create chart source directory")
	}

	templatesDir := filepath.Join(chartSourcePath, "templates")
	if err := os.MkdirAll(templatesDir, 0755); err != nil {
		return "", errors.Wrap(err, "failed to create templates directory")
	}

	chartMeta := &chart.Metadata{
		APIVersion:  "v2",
		Name:        chartName,
		Version:     chartVersion,
		Description: fmt.Sprintf("Helm chart for '%s' generated by Meshery", chartName),
		Type:        "application",
	}

	chartYamlContent, err := yaml.Marshal(chartMeta)
	if err != nil {
		return "", errors.Wrap(err, "failed to marshal Chart.yaml metadata")
	}

	if err := os.WriteFile(filepath.Join(chartSourcePath, "Chart.yaml"), chartYamlContent, 0644); err != nil {
		return "", errors.Wrap(err, "failed to write Chart.yaml")
	}

	valuesContent := []byte("# Default values for " + chartName + "\nglobal:\n  namespace: default\n")
	if err := os.WriteFile(filepath.Join(chartSourcePath, "values.yaml"), valuesContent, 0644); err != nil {
		return "", errors.Wrap(err, "failed to write values.yaml")
	}

	if err := os.WriteFile(filepath.Join(templatesDir, "manifest.yaml"), []byte(manifestContent), 0644); err != nil {
		return "", errors.Wrap(err, "failed to write manifest.yaml")
	}

// helpersContent := `{{/* Generate basic chart labels */}}
// {{- define "chart.labels" }}
// helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
// app.kubernetes.io/managed-by: {{ .Release.Service }}
// app.kubernetes.io/instance: {{ .Release.Name }}
// app.kubernetes.io/name: {{ include "chart.name" . }}
// {{- end }}

// {{/* Define chart name */}}
// {{- define "chart.name" }}
// {{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
// {{- end }}
// `
	// if err := os.WriteFile(filepath.Join(templatesDir, "_helpers.tpl"), []byte(helpersContent), 0644); err != nil {
	// 	return "", errors.Wrap(err, "failed to write _helpers.tpl")
	// }

	// notesContent := fmt.Sprintf("This Helm chart '%s' was generated by Meshery.\n", chartName)
	// if err := os.WriteFile(filepath.Join(chartSourcePath, "NOTES.txt"), []byte(notesContent), 0644); err != nil {
	// 	return "", errors.Wrap(err, "failed to write NOTES.txt")
	// }

	packager := action.NewPackage()
	packager.Destination = packageDir

	fmt.Printf("Packaging chart from: %s to: %s\n", chartSourcePath, packageDir)

	packagedChartPath, err := packager.Run(chartSourcePath, nil)
	if err != nil {
		return "", errors.Wrap(err, "helm packaging failed")
	}

	chartData, err := os.ReadFile(packagedChartPath)
	if err != nil {
		return "", errors.Wrap(err, "failed to read packaged chart")
	}

	fmt.Printf("Packaged chart size: %d bytes\n", len(chartData))

	if err := os.Remove(packagedChartPath); err != nil {
		fmt.Printf("Warning: Failed to clean up packaged chart: %+v\n", errors.Wrap(err, "failed to remove packaged chart"))	
	}

	return string(chartData), nil
}

func sanitizeHelmName(name string) string {
    if name == "" {
        return "meshery-design"
    }

    result := strings.ToLower(name)
    reg := regexp.MustCompile(`[^a-z0-9-]+`)
    result = reg.ReplaceAllString(result, "-")

    for strings.Contains(result, "--") {
        result = strings.ReplaceAll(result, "--", "-")
    }

    result = strings.Trim(result, "-")

    if result == "" {
        return "meshery-design"
    }

    const maxLength = 40
    if len(result) > maxLength {
        result = result[:maxLength]

        result = strings.Trim(result, "-")
    }

    return result
}