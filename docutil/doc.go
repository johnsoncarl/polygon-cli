package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// genMarkdownDoc will generate Markdown documentation for this command and all descendants in the
// directory given. This is a modified fork of Cobra's `GenMarkdownTreeCustom` function.
// https://pkg.go.dev/github.com/spf13/cobra/doc#GenMarkdownTree
func genMarkdownDoc(cmd *cobra.Command, dir string) error {
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := genMarkdownDoc(c, dir); err != nil {
			return err
		}
	}

	basename := strings.ReplaceAll(cmd.CommandPath(), " ", "_") + ".md"
	filename := filepath.Join(dir, basename)
	f, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := genMarkdownPage(cmd, f); err != nil {
		return err
	}
	return nil
}

// genMarkdownPage will generate a Markdown page for this command.
// This is a modified version of Cobra's `GenMarkdownTreeCustom` function.
// https://pkg.go.dev/github.com/spf13/cobra/doc#GenMarkdownTreeCustom
func genMarkdownPage(cmd *cobra.Command, w io.Writer) error {
	cmd.InitDefaultHelpCmd()
	cmd.InitDefaultHelpFlag()

	buf := new(bytes.Buffer)
	name := cmd.CommandPath()

	short := cmd.Short

	buf.WriteString("# `" + name + "`\n\n")

	if !cmd.DisableAutoGenTag {
		buf.WriteString("> Auto-generated documentation.\n\n")
	}

	printToC(buf, cmd)

	buf.WriteString("## Description\n\n")
	buf.WriteString(short + "\n\n")
	if cmd.Runnable() {
		buf.WriteString(fmt.Sprintf("```bash\n%s\n```\n\n", cmd.UseLine()))
	}

	if len(cmd.Long) != 0 {
		buf.WriteString("## Usage\n\n")
		buf.WriteString(cmd.Long + "\n")
	}

	if err := printFlags(buf, cmd, name); err != nil {
		return err
	}

	if len(cmd.Example) > 0 {
		buf.WriteString("## Examples\n\n")
		buf.WriteString(fmt.Sprintf("```bash\n%s\n```\n\n", cmd.Example))
	}

	if hasSeeAlso(cmd) {
		buf.WriteString("## See also\n")
		identity := func(s string) string { return s }
		printSeeAlso(buf, cmd, name, identity)
	}

	_, err := buf.WriteTo(w)
	return err
}

// Print the table of content of a command markdown page.
func printToC(buf *bytes.Buffer, cmd *cobra.Command) {
	buf.WriteString("## Table of Contents\n\n")
	buf.WriteString("- [Description](#description)\n")
	buf.WriteString("- [Usage](#usage)\n")
	buf.WriteString("- [Flags](#flags)\n")
	if len(cmd.Example) > 0 {
		buf.WriteString("- [Examples](#examples)\n")
	}
	if hasSeeAlso(cmd) {
		buf.WriteString("- [See Also](#see-also)\n")
	}
	buf.WriteString("\n")
}

// Print the command flags. This is a modified fork of Cobra's `printOptions` function.
func printFlags(buf *bytes.Buffer, cmd *cobra.Command, name string) error {
	flags := cmd.NonInheritedFlags()
	parentFlags := cmd.InheritedFlags()
	if flags.HasAvailableFlags() || parentFlags.HasAvailableFlags() {
		buf.WriteString("## Flags")
	}

	flags.SetOutput(buf)
	if flags.HasAvailableFlags() {
		buf.WriteString("\n\n")
		buf.WriteString("```bash\n")
		flags.PrintDefaults()
		buf.WriteString("```\n\n")
	}

	parentFlags.SetOutput(buf)
	if parentFlags.HasAvailableFlags() {
		buf.WriteString("The command also inherits flags from parent commands.\n\n")
		buf.WriteString("```bash\n")
		parentFlags.PrintDefaults()
		buf.WriteString("```\n\n")
	}
	return nil
}

// Test to see if we have a reason to print See Also information in docs.
// Basically this is a test for a parent commend or a subcommand which is
// both not deprecated and not the autogenerated help command.
// This is a fork of Cobra's `hasSeeAlso` function.
func hasSeeAlso(cmd *cobra.Command) bool {
	if cmd.HasParent() {
		return true
	}
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
			continue
		}
		return true
	}
	return false
}

// Print See Also section.
func printSeeAlso(buf *bytes.Buffer, cmd *cobra.Command, name string, linkHandler func(s string) string) {
	if cmd.HasParent() {
		parent := cmd.Parent()
		pname := parent.CommandPath()
		link := pname + ".md"
		link = strings.Replace(link, " ", "_", -1)
		buf.WriteString(fmt.Sprintf("\n- [%s](%s) - %s", pname, linkHandler(link), parent.Short))
		cmd.VisitParents(func(c *cobra.Command) {
			if c.DisableAutoGenTag {
				cmd.DisableAutoGenTag = c.DisableAutoGenTag
			}
		})
	}

	children := cmd.Commands()
	sort.Sort(byName(children))

	for _, child := range children {
		if !child.IsAvailableCommand() || child.IsAdditionalHelpTopicCommand() {
			continue
		}
		cname := name + " " + child.Name()
		link := cname + ".md"
		link = strings.Replace(link, " ", "_", -1)
		buf.WriteString(fmt.Sprintf("\n- [%s](%s) - %s\n", cname, linkHandler(link), child.Short))
	}
	buf.WriteString("\n")
}

type byName []*cobra.Command

func (s byName) Len() int           { return len(s) }
func (s byName) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s byName) Less(i, j int) bool { return s[i].Name() < s[j].Name() }