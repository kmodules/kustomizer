package main

import (
	"flag"
	"fmt"
	"github.com/spf13/cobra"
	"io/ioutil"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"os"
	"path/filepath"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/yaml"
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "gen input_dir",
		Short: "Generate kustomization.yaml",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: gen input_dir")
			}

			return generate(args[0])
		},
	}
	rootCmd.Flags().AddGoFlagSet(flag.CommandLine)
	utilruntime.Must(flag.CommandLine.Parse([]string{}))

	utilruntime.Must(rootCmd.Execute())
}

func generate(dir string) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if path == dir {
			return nil
		}

		// TODO: Detect base folder
		rel, err := filepath.Rel(path, filepath.Join(dir, "base"))
		if err != nil {
			return err
		}

		entries, err := ioutil.ReadDir(path)
		if err != nil {
			return err
		}

		var resources []string
		for _, e := range entries {
			if !e.IsDir() && e.Name() != "kustomization.yaml" {
				resources = append(resources, e.Name())
			}
		}
		if len(resources) == 0 {
			return nil
		}
		cfg := types.Kustomization{
			TypeMeta: types.TypeMeta{
				APIVersion: types.KustomizationVersion,
				Kind:       types.KustomizationKind,
			},
			Resources: resources,
		}
		if rel != "." {
			cfg.Bases = []string{rel}
		}
		data, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}
		return ioutil.WriteFile(filepath.Join(path, "kustomization.yaml"), data, 0644)
	})
}