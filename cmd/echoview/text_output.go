package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/dayflower/echoview/internal/catalog"
	"github.com/dayflower/echoview/internal/discover"
	"github.com/dayflower/echoview/internal/echonet"
	"github.com/dayflower/echoview/internal/model"
)

func printNodes(output io.Writer, interfaceAddress fmt.Stringer, nodes []discover.Node, loadedCatalog *catalog.Catalog, locale catalog.Locale) {
	fmt.Fprintf(output, "Interface: %s\n", interfaceAddress)
	fmt.Fprintf(output, "Discovered nodes: %d\n", len(nodes))
	for _, node := range nodes {
		fmt.Fprintf(output, "\n%s\n", node.Address)
		fmt.Fprintf(output, "  Node profile: %s%s\n", node.NodeProfileEOJ, classSuffix(loadedCatalog, node.NodeProfileEOJ, locale))
		printProperty(output, "  ", node.NodeProfileEOJ, node.NodeProperties[0x83], loadedCatalog, locale)
		printProperty(output, "  ", node.NodeProfileEOJ, node.NodeProperties[0xD3], loadedCatalog, locale)
		printProperty(output, "  ", node.NodeProfileEOJ, node.NodeProperties[0xD4], loadedCatalog, locale)
		for _, eoj := range sortedInstanceEOJs(node) {
			if eoj == node.NodeProfileEOJ {
				continue
			}
			fmt.Fprintf(output, "  - %s%s\n", eoj, classSuffix(loadedCatalog, eoj, locale))
			properties := node.InstanceProperties[eoj]
			printProperty(output, "      ", eoj, properties[0x80], loadedCatalog, locale)
			printOptionalProperty(output, "      ", eoj, properties[0x81], loadedCatalog, locale)
			printOptionalProperty(output, "      ", eoj, properties[0x8A], loadedCatalog, locale)
			printOptionalProperty(output, "      ", eoj, properties[0x8B], loadedCatalog, locale)
			printOptionalProperty(output, "      ", eoj, properties[0x8C], loadedCatalog, locale)
			printOptionalProperty(output, "      ", eoj, properties[0x8D], loadedCatalog, locale)
		}
	}
}

func sortedInstanceEOJs(node discover.Node) []echonet.EOJ {
	result := make([]echonet.EOJ, 0, len(node.Instances))
	for eoj := range node.Instances {
		result = append(result, eoj)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result
}

func classSuffix(loadedCatalog *catalog.Catalog, eoj echonet.EOJ, locale catalog.Locale) string {
	class, ok := loadedCatalog.ClassForEOJ(eoj)
	name := catalog.Localized{JA: class.NameJA, EN: class.NameEN}.Value(locale)
	if !ok || name == "" {
		return ": Unknown class"
	}
	if class.ShortName == "" {
		return ": " + name
	}
	return fmt.Sprintf(": %s [%s]", name, class.ShortName)
}

func printProperty(output io.Writer, indent string, eoj echonet.EOJ, property discover.Property, loadedCatalog *catalog.Catalog, locale catalog.Locale) {
	definition, known := loadedCatalog.PropertyForEOJ(eoj, property.EPC)
	name := echonet.FormatEPC(property.EPC)
	if known {
		if localized := (catalog.Localized{JA: definition.NameJA, EN: definition.NameEN}).Value(locale); localized != "" {
			name = localized
		}
	}
	if property.EDT == nil {
		if property.Status == discover.StatusSNA {
			fmt.Fprintf(output, "%s%s (0x%02X): unavailable\n", indent, name, property.EPC)
		} else {
			fmt.Fprintf(output, "%s%s (0x%02X): unavailable (%s)\n", indent, name, property.EPC, property.Status)
		}
		return
	}
	decoded := loadedCatalog.DecodeProperty(eoj, property.EPC, property.EDT, locale)
	value := decoded.Raw
	if decoded.Decoded {
		value = fmt.Sprint(decoded.Value) + " (" + decoded.Raw + ")"
	}
	if decoded.Unit != "" {
		value += " " + decoded.Unit
	}
	if property.Status != discover.StatusOK {
		value += " (" + string(property.Status) + ")"
	}
	fmt.Fprintf(output, "%s%s (0x%02X): %s\n", indent, name, property.EPC, value)
}

func printOptionalProperty(output io.Writer, indent string, eoj echonet.EOJ, property discover.Property, loadedCatalog *catalog.Catalog, locale catalog.Locale) {
	if property.EDT != nil {
		printProperty(output, indent, eoj, property, loadedCatalog, locale)
	}
}

func printDumpResults(output io.Writer, interfaceAddress fmt.Stringer, nodes []model.NodeResult, showRaw bool, locale catalog.Locale) {
	fmt.Fprintf(output, "Interface: %s\n", interfaceAddress)
	fmt.Fprintf(output, "Nodes: %d\n", len(nodes))
	for _, node := range nodes {
		fmt.Fprintf(output, "\n%s\n", node.Address)
		if len(node.NodeProfile.GetProperties) > 0 {
			printDumpInstance(output, "  Node profile", node.NodeProfile, showRaw, locale)
		}
		for _, instance := range node.Instances {
			printDumpInstance(output, "  -", instance, showRaw, locale)
		}
	}
}

func printDumpInstance(output io.Writer, prefix string, instance model.InstanceResult, showRaw bool, locale catalog.Locale) {
	suffix := ""
	if className := localizedText(locale, instance.ClassName, instance.ClassNameEN, ""); className != "" {
		suffix = ": " + className
		if instance.ClassShortName != nil {
			suffix += " [" + *instance.ClassShortName + "]"
		}
	}
	if instance.Profile != nil {
		suffix += " [profile: " + instance.Profile.ID + " (" + instance.Profile.MatchState + ")]"
	}
	fmt.Fprintf(output, "%s %s%s\n", prefix, instance.EOJ, suffix)
	for _, property := range instance.GetProperties {
		name := property.EPC
		if localized := localizedText(locale, property.NameJA, property.NameEN, ""); localized != "" {
			name = localized
		}
		if property.EPC == "0x9E" || property.EPC == "0x9F" {
			line := fmt.Sprintf("      %s (%s):", name, property.EPC)
			if showRaw && property.RawEDT != nil {
				line += " raw: " + *property.RawEDT
			}
			fmt.Fprintln(output, line)
			continue
		}
		if property.RawEDT == nil {
			fmt.Fprintf(output, "      %s (%s): unavailable (%s)\n", name, property.EPC, property.Status)
			continue
		}
		value := fmt.Sprint(property.Value)
		if showRaw {
			value += " (raw: " + *property.RawEDT + ")"
		}
		if property.Unit != nil {
			value += " " + *property.Unit
		}
		if property.Status != model.PropertyOK {
			value += " (" + string(property.Status) + ")"
		}
		fmt.Fprintf(output, "      %s (%s): %s\n", name, property.EPC, value)
	}
}

func localizedText(locale catalog.Locale, japanese, english *string, fallback string) string {
	value := catalog.Localized{}
	if japanese != nil {
		value.JA = *japanese
	}
	if english != nil {
		value.EN = *english
	}
	if text := value.Value(locale); text != "" {
		return text
	}
	return fallback
}
