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
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gomodules.xyz/jsonpatch/v3"
	ioutil2 "gomodules.xyz/x/ioutil"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/kustomize/api/resid"
	"sigs.k8s.io/kustomize/api/types"
	yaml2 "sigs.k8s.io/yaml"
)

type Variable struct {
	Base string `json:"base,omitempty"`
	Dir  string `json:"dir,omitempty"`
	Fork bool   `json:"fork,omitempty"`
}

type Profile []Variable

type Kustomizer struct {
	Profiles map[string]Profile `json:"profiles"`
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "kustomizer input_dir output_dir",
		Short: "Generate json patch",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 2 {
				return fmt.Errorf("usage: kustomizer input_dir output_dir")
			}

			rootDir := args[0]
			dstDir := args[1]

			err := os.MkdirAll(dstDir, 0o755)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(filepath.Join(rootDir, "kustomizer.yaml"))
			if err != nil {
				return err
			}
			var cfg Kustomizer
			err = yaml2.Unmarshal(data, &cfg)
			if err != nil {
				return err
			}

			for profile, v := range cfg.Profiles {
				fmt.Println("processing profile", profile)
				err = ProcessDir(rootDir, "", dstDir, v)
				if err != nil {
					return err
				}
			}
			return nil
		},
	}
	rootCmd.Flags().AddGoFlagSet(flag.CommandLine)
	utilruntime.Must(flag.CommandLine.Parse([]string{}))

	utilruntime.Must(rootCmd.Execute())
}

type ObjKey struct {
	APIVersion string
	Kind       string
	Name       string
	Namespace  string
}

func ProcessDir(rootDir, dstBase, dstDir string, vars []Variable) error {
	if len(vars) == 0 {
		return nil
	}
	if vars[0].Base != "" {
		nextDstDir := filepath.Join(dstDir, filepath.Base(vars[0].Base))
		if len(vars) > 1 && filepath.Base(nextDstDir) != "base" {
			nextDstDir = filepath.Join(nextDstDir, "base")
		}
		err := ProcessBaseDir(rootDir, vars[0].Base, dstBase, nextDstDir)
		if err != nil {
			return err
		}
		err = ProcessDir(rootDir, nextDstDir, filepath.Dir(nextDstDir), vars[1:])
		if err != nil {
			return err
		}
		if len(vars) > 2 && vars[1].Fork {
			err = ProcessDir(rootDir, nextDstDir, filepath.Dir(nextDstDir), vars[2:])
			if err != nil {
				return err
			}
		}
		return nil
	} else if vars[0].Dir != "" {
		dirVars, err := ioutil.ReadDir(filepath.Join(rootDir, vars[0].Dir))
		if err != nil {
			return err
		}
		for _, dirVar := range dirVars {
			if dirVar.IsDir() {
				nextDstDir := filepath.Join(dstDir, dirVar.Name())
				if len(vars) > 1 {
					nextDstDir = filepath.Join(nextDstDir, "base")
				}
				err := ProcessBaseDir(filepath.Join(rootDir, vars[0].Dir), dirVar.Name(), dstBase, nextDstDir)
				if err != nil {
					return err
				}
				err = ProcessDir(rootDir, nextDstDir, filepath.Dir(nextDstDir), vars[1:])
				if err != nil {
					return err
				}
				if len(vars) > 2 && vars[1].Fork {
					err = ProcessDir(rootDir, nextDstDir, filepath.Dir(nextDstDir), vars[2:])
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func ProcessBaseDir(rootDir string, xBase string, dstBase, dstDir string) error {
	var srcCfg *types.Kustomization
	srcKustomization := filepath.Join(rootDir, xBase, "kustomization.yaml")
	srcCfg, err := LoadKustomization(srcKustomization)
	if err != nil {
		return err
	}
	if len(srcCfg.Bases) == 0 {
		return ioutil2.CopyDir(dstDir, filepath.Join(rootDir, xBase), ioutil2.IgnoreDestination())
	} else if len(srcCfg.Bases) > 1 {
		return fmt.Errorf("%s has more than one bases", srcKustomization)
	}
	targetResources := map[ObjKey]*unstructured.Unstructured{}

	for _, res := range srcCfg.Resources {
		data, err := os.ReadFile(filepath.Join(rootDir, xBase, res))
		if err != nil {
			return err
		}
		reader := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 2048)
		for {
			var obj unstructured.Unstructured
			err := reader.Decode(&obj)
			if err == io.EOF {
				break
			} else if err != nil {
				return err
			}
			if obj.IsList() {
				err := obj.EachListItem(func(item runtime.Object) error {
					castItem := item.(*unstructured.Unstructured)
					targetResources[ObjKey{
						APIVersion: castItem.GetAPIVersion(),
						Kind:       castItem.GetKind(),
						Name:       castItem.GetName(),
						Namespace:  castItem.GetNamespace(),
					}] = castItem
					return nil
				})
				if err != nil {
					return err
				}
			} else {
				targetResources[ObjKey{
					APIVersion: obj.GetAPIVersion(),
					Kind:       obj.GetKind(),
					Name:       obj.GetName(),
					Namespace:  obj.GetNamespace(),
				}] = &obj
			}
		}
	}

	baseDir := filepath.Join(rootDir, xBase, srcCfg.Bases[0])
	baseKustomization := filepath.Join(baseDir, "kustomization.yaml")
	baseCfg, err := LoadKustomization(baseKustomization)
	if err != nil {
		return err
	}
	baseResources := map[ObjKey]*unstructured.Unstructured{}
	for _, res := range baseCfg.Resources {
		data, err := os.ReadFile(filepath.Join(baseDir, res))
		if err != nil {
			return err
		}
		reader := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 2048)
		for {
			var obj unstructured.Unstructured
			err := reader.Decode(&obj)
			if err == io.EOF {
				break
			} else if err != nil {
				return err
			}
			if obj.IsList() {
				err := obj.EachListItem(func(item runtime.Object) error {
					castItem := item.(*unstructured.Unstructured)
					baseResources[ObjKey{
						APIVersion: castItem.GetAPIVersion(),
						Kind:       castItem.GetKind(),
						Name:       castItem.GetName(),
						Namespace:  castItem.GetNamespace(),
					}] = castItem
					return nil
				})
				if err != nil {
					return err
				}
			} else {
				baseResources[ObjKey{
					APIVersion: obj.GetAPIVersion(),
					Kind:       obj.GetKind(),
					Name:       obj.GetName(),
					Namespace:  obj.GetNamespace(),
				}] = &obj
			}
		}
	}

	shortNames := sets.NewString()
	var shortNameConflict bool
	mediumNames := sets.NewString()
	var mediumNameConflict bool
	longNames := sets.NewString()
	var longNameConflict bool
	for objKey, targetResource := range targetResources {
		if baseResource, ok := baseResources[objKey]; ok {
			// generate patch
			if IsOfficialType(objKey.APIVersion) {
				shortName := "overlay.yaml"
				mediumName := fmt.Sprintf("%s-overlay.yaml", baseResource.GetName())
				longName := fmt.Sprintf("%s-%s-overlay.yaml", baseResource.GetName(), strings.ToLower(baseResource.GetKind()))

				if shortNames.Has(shortName) {
					shortNameConflict = true
				} else {
					shortNames.Insert(shortName)
				}

				if mediumNames.Has(mediumName) {
					mediumNameConflict = true
				} else {
					mediumNames.Insert(mediumName)
				}

				if longNames.Has(longName) {
					longNameConflict = true
				} else {
					longNames.Insert(longName)
				}
			} else {
				shortName := "patch.yaml"
				mediumName := fmt.Sprintf("%s-patch.yaml", baseResource.GetName())
				longName := fmt.Sprintf("%s-%s-patch.yaml", baseResource.GetName(), strings.ToLower(baseResource.GetKind()))

				if shortNames.Has(shortName) {
					shortNameConflict = true
				} else {
					shortNames.Insert(shortName)
				}

				if mediumNames.Has(mediumName) {
					mediumNameConflict = true
				} else {
					mediumNames.Insert(mediumName)
				}

				if longNames.Has(longName) {
					longNameConflict = true
				} else {
					longNames.Insert(longName)
				}
			}
		} else {
			// add resource
			shortName := fmt.Sprintf("%s.yaml", targetResource.GetName())
			mediumName := fmt.Sprintf("%s.yaml", targetResource.GetName())
			longName := fmt.Sprintf("%s-%s.yaml", targetResource.GetName(), strings.ToLower(targetResource.GetKind()))

			if shortNames.Has(shortName) {
				shortNameConflict = true
			} else {
				shortNames.Insert(shortName)
			}

			if mediumNames.Has(mediumName) {
				mediumNameConflict = true
			} else {
				mediumNames.Insert(mediumName)
			}

			if longNames.Has(longName) {
				longNameConflict = true
			} else {
				longNames.Insert(longName)
			}
		}
	}

	const (
		shortName  = "short"
		mediumName = "medium"
		longName   = "long"
	)
	var namesize string
	if !shortNameConflict {
		namesize = shortName
	} else if !mediumNameConflict {
		namesize = mediumName
	} else if !longNameConflict {
		namesize = longName
	} else {
		return fmt.Errorf("naming conflict for rootDir=%s variable=%#v", rootDir, xBase)
	}

	targetCfg := types.Kustomization{
		TypeMeta: types.TypeMeta{
			APIVersion: types.KustomizationVersion,
			Kind:       types.KustomizationKind,
		},
	}
	if dstBase != "" {
		relativeBase, err := filepath.Rel(dstDir, dstBase)
		if err != nil {
			return err
		}
		targetCfg.Bases = []string{relativeBase}
	}

	for objKey, targetResource := range targetResources {
		if baseResource, ok := baseResources[objKey]; ok {
			// generate patch
			if IsOfficialType(objKey.APIVersion) {
				var name string
				if namesize == shortName {
					name = "overlay.yaml"
				} else if namesize == mediumName {
					name = fmt.Sprintf("%s-overlay.yaml", baseResource.GetName())
				} else if namesize == longName {
					name = fmt.Sprintf("%s-%s-overlay.yaml", baseResource.GetName(), strings.ToLower(baseResource.GetKind()))
				}

				data, err := generateStrategicMergePatch(baseResource, targetResource)
				if err != nil {
					return err
				}
				err = os.MkdirAll(dstDir, 0o755)
				if err != nil {
					return err
				}
				err = os.WriteFile(filepath.Join(dstDir, name), data, 0o644)
				if err != nil {
					return err
				}
				targetCfg.PatchesStrategicMerge = append(targetCfg.PatchesStrategicMerge, types.PatchStrategicMerge(name))
			} else {
				var name string
				if namesize == shortName {
					name = "patch.yaml"
				} else if namesize == mediumName {
					name = fmt.Sprintf("%s-patch.yaml", baseResource.GetName())
				} else if namesize == longName {
					name = fmt.Sprintf("%s-%s-patch.yaml", baseResource.GetName(), strings.ToLower(baseResource.GetKind()))
				}

				err = os.MkdirAll(dstDir, 0o755)
				if err != nil {
					return err
				}

				patch, err := generateJsonPatch(baseResource, targetResource)
				if err != nil {
					return err
				}
				if len(patch) > 0 {
					data, err := yaml2.Marshal(patch)
					if err != nil {
						return err
					}
					err = os.WriteFile(filepath.Join(dstDir, name), data, 0o644)
					if err != nil {
						return err
					}

					gv, err := schema.ParseGroupVersion(objKey.APIVersion)
					if err != nil {
						return err
					}
					targetCfg.PatchesJson6902 = append(targetCfg.PatchesJson6902, types.Patch{
						Target: &types.Selector{
							Gvk: resid.Gvk{
								Group:   gv.Group,
								Version: gv.Version,
								Kind:    objKey.Kind,
							},
							Namespace: objKey.Namespace,
							Name:      objKey.Name,
						},
						Path: name,
					})
				}
			}
		} else {
			// add resource
			var name string
			if namesize == shortName {
				name = fmt.Sprintf("%s.yaml", targetResource.GetName())
			} else if namesize == "" {
				name = fmt.Sprintf("%s.yaml", targetResource.GetName())
			} else if namesize == longName {
				name = fmt.Sprintf("%s-%s.yaml", targetResource.GetName(), strings.ToLower(targetResource.GetKind()))
			}

			data, err := yaml2.Marshal(targetResource)
			if err != nil {
				return err
			}
			err = os.MkdirAll(dstDir, 0o755)
			if err != nil {
				return err
			}
			err = os.WriteFile(filepath.Join(dstDir, name), data, 0o644)
			if err != nil {
				return err
			}

			targetCfg.Resources = append(targetCfg.Resources, name)
		}
	}

	sort.Strings(targetCfg.Resources)
	targetKustomization := filepath.Join(dstDir, "kustomization.yaml")
	data, err := yaml2.Marshal(targetCfg)
	if err != nil {
		return err
	}
	return os.WriteFile(targetKustomization, data, 0o644)
}

func LoadKustomization(filename string) (*types.Kustomization, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	var cfg types.Kustomization
	err = yaml2.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func IsOfficialType(apiVersion string) bool {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		panic(err)
	}
	return gv.Group == "" ||
		!strings.ContainsRune(gv.Group, '.') ||
		strings.HasSuffix(gv.Group, ".k8s.io")
}

func generateJsonPatch(fromObj, toObj *unstructured.Unstructured) ([]jsonpatch.Operation, error) {
	fromJson, err := json.Marshal(fromObj)
	if err != nil {
		return nil, err
	}

	toJson, err := json.Marshal(toObj)
	if err != nil {
		return nil, err
	}

	return jsonpatch.CreatePatch(fromJson, toJson)
}

func generateStrategicMergePatch(fromObj, toObj *unstructured.Unstructured) ([]byte, error) {
	obj, err := scheme.Scheme.New(fromObj.GetObjectKind().GroupVersionKind())
	if err != nil {
		return nil, err
	}

	overlay, err := strategicpatch.CreateTwoWayMergeMapPatch(fromObj.Object, toObj.Object, obj)
	if err != nil {
		return nil, err
	}

	overlay["apiVersion"] = fromObj.GetAPIVersion()
	overlay["kind"] = fromObj.GetKind()
	err = unstructured.SetNestedField(overlay, fromObj.GetName(), "metadata", "name")
	if err != nil {
		return nil, err
	}

	return yaml2.Marshal(overlay)
}
