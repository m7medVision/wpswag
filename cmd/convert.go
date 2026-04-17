package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/m7medVision/wpswag/internal/convert"
	"github.com/m7medVision/wpswag/internal/tag"
	"github.com/m7medVision/wpswag/internal/util"
	"github.com/m7medVision/wpswag/internal/wp"
)

var (
	flagURL        string
	flagOut        string
	flagDebug      bool
	flagNoTag      bool
	flagDryRun     bool
	flagCategories string
)

var convertCmd = &cobra.Command{
	Use:   "convert",
	Short: "Convert WordPress REST API JSON to OpenAPI 3.0 spec",
	Long:  "Fetch a WordPress REST API index (URL or local JSON file) and generate an OpenAPI 3.0.3 JSON specification.",
	RunE:  runConvert,
}

func init() {
	convertCmd.Flags().StringVarP(&flagURL, "url", "u", "", "WordPress REST URL or local JSON file (e.g. https://site/wp-json or ./wp-json.json)")
	convertCmd.Flags().StringVarP(&flagOut, "output", "o", "openapi.json", "Output OpenAPI file (default: openapi.json)")
	convertCmd.Flags().BoolVar(&flagDebug, "debug", false, "Print debug stats to stderr")
	convertCmd.Flags().BoolVar(&flagNoTag, "no-tag", false, "Skip AI tagging (output spec with empty tags)")
	convertCmd.Flags().BoolVar(&flagDryRun, "dry-run", false, "Estimate token cost without calling opencode")
	convertCmd.Flags().StringVar(&flagCategories, "categories", "", "Custom categories JSON file (default: built-in WordPress categories)")
	_ = convertCmd.MarkFlagRequired("url")
	rootCmd.AddCommand(convertCmd)
}

func runConvert(cmd *cobra.Command, args []string) error {
	data, err := util.Fetch(flagURL)
	if err != nil {
		return fmt.Errorf("fetch error: %w", err)
	}
	data = util.CleanJSON(data)

	var idx wp.Index
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&idx); err != nil {
		return fmt.Errorf("decode error: %w", err)
	}

	conv := convert.NewConverter(&idx, flagURL)
	spec, stats, err := conv.Convert()
	if err != nil {
		fmt.Fprintf(os.Stderr, "convert error: %v\n", err)
	}

	if flagDebug {
		fmt.Fprintf(os.Stderr, "routes=%d endpoints=%d ops=%d paths_out=%d\n",
			stats.Routes, stats.Endpoints, stats.Ops, len(spec.Paths))
	}

	if !flagNoTag {
		categories := tag.DefaultCategories
		if flagCategories != "" {
			categories, err = loadCategories(flagCategories)
			if err != nil {
				return fmt.Errorf("categories: %w", err)
			}
		}
		if err := tag.TagSpec(spec, categories, flagDryRun); err != nil {
			return fmt.Errorf("tag: %w", err)
		}
		if flagDryRun {
			return nil
		}
	}

	out, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal error: %w", err)
	}

	if err := os.WriteFile(flagOut, out, 0644); err != nil {
		return fmt.Errorf("write error: %w", err)
	}

	fmt.Fprintf(os.Stderr, "wrote %s (%s bytes)\n", filepath.Base(flagOut), strconv.Itoa(len(out)))
	return nil
}

func loadCategories(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cats []string
	if err := json.Unmarshal(data, &cats); err != nil {
		return nil, err
	}
	return cats, nil
}
