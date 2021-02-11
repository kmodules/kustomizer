/*
Copyright AppsCode Inc. and Contributors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	shell "github.com/codeskyblue/go-sh"
	"github.com/spf13/cobra"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "build input_dir out_dir",
		Short: "Build yamls",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("usage: build input_dir output_dir")
			}

			return build(args[0], args[1])
		},
	}
	rootCmd.Flags().AddGoFlagSet(flag.CommandLine)
	utilruntime.Must(flag.CommandLine.Parse([]string{}))

	utilruntime.Must(rootCmd.Execute())
}

func build(in, out string) error {
	sh := shell.NewSession()
	// sh.ShowCMD = true

	return filepath.Walk(in, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if path == in {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "kustomization.yaml")); os.IsNotExist(err) {
			return nil
		}

		// TODO: Detect base folder
		rel, err := filepath.Rel(in, path)
		if err != nil {
			return err
		}

		fmt.Println(path)

		sh.SetDir(path)
		data, err := sh.Command("kustomize", "build").Output()
		if err != nil {
			return err
		}

		err = os.MkdirAll(filepath.Join(out, rel), 0755)
		if err != nil {
			return err
		}

		return ioutil.WriteFile(filepath.Join(out, rel, "sample.yaml"), data, 0644)
	})
}
