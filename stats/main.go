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
	"sort"
	"text/tabwriter"

	"kmodules.xyz/client-go/tools/parser"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

type Stats struct {
	Config string
	Count  int
}

var store []Stats

func main() {
	var rootCmd = &cobra.Command{
		Use:   "main input_dir",
		Short: "Show number of resources for each configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("usage: stats input_dir")
			}

			return calculate(args[0])
		},
	}
	rootCmd.Flags().AddGoFlagSet(flag.CommandLine)
	utilruntime.Must(flag.CommandLine.Parse([]string{}))

	utilruntime.Must(rootCmd.Execute())
}

func calculate(in string) error {
	err := filepath.Walk(in, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return nil
		}
		if path == in {
			return nil
		}
		filename := filepath.Join(path, "sample.yaml")
		if _, err := os.Stat(filename); os.IsNotExist(err) {
			return nil
		}

		// TODO: Detect base folder
		rel, err := filepath.Rel(in, path)
		if err != nil {
			return err
		}

		data, err := ioutil.ReadFile(filename)
		if err != nil {
			return err
		}

		count := 0
		err = parser.ProcessResources(data, func(obj *unstructured.Unstructured) error {
			count++
			return nil
		})
		if err != nil {
			return err
		}

		store = append(store, Stats{
			Config: rel,
			Count:  count,
		})
		return nil
	})
	if err != nil {
		return err
	}

	sort.Slice(store, func(i, j int) bool {
		if store[i].Count == store[j].Count {
			return store[i].Config < store[j].Config
		}
		return store[i].Count > store[j].Count
	})

	// Observe how the b's and the d's, despite appearing in the
	// second cell of each line, belong to different columns.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 1, ' ', tabwriter.TabIndent)
	for _, cfg := range store {
		_, _ = fmt.Fprintf(w, "%s\t%d\n", cfg.Config, cfg.Count)
	}
	return w.Flush()
}
