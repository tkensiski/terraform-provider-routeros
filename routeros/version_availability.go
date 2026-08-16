package routeros

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

// Attribute availability per RouterOS version.
//
// RouterOS both adds and removes menu properties between releases. Terraform
// asks a provider for its resource schemas before the provider client exists,
// so the schema itself cannot vary by device version -- it stays a superset.
// What the provider *can* do is check whether an attribute the user actually
// set exists on the router it is talking to. RouterOSVersion is populated when
// the client is configured (NewClient auto-detects it from /system/resource
// unless the `routeros_version` setting overrides it), which is the same signal
// the attribute-drift compensation in mikrotik_serialize.go already relies on.
// Because the provider is configured before planning, the check can run during
// plan rather than waiting for apply.
//
// This matters most for removed properties. RouterOS accepts a write to a
// property it no longer supports, answers HTTP 200, and then omits it from
// every read -- so Terraform records the user's value, sees nothing to compare
// it against, and reports no drift forever. Without a check like this the
// setting is silently discarded.

type attrVersions struct {
	// Min is the first RouterOS release that has the property, e.g. "7.23".
	// Empty means the property has always been present.
	Min string
	// Max is the last RouterOS release that has it. Empty means still present.
	Max string
}

// attributeAvailability maps a resource path to the attributes whose support is
// bounded, keyed by their Terraform (snake_case) names.
//
// Bounds should come from measurement against real RouterOS builds rather than
// documentation; see test/chr for a harness that reports which properties each
// version actually returns.
var attributeAvailability = map[string]map[string]attrVersions{
	"/interface/bridge": {
		// Measured: present on 7.16.2, absent from /interface/bridge on 7.23.
		// The exact removal release still needs narrowing.
		"mvrp":            {Min: "7.15", Max: "7.22"},
		"dhcpv6_snooping": {Min: "7.23"},
		"ra_guard":        {Min: "7.22"},
	},
	"/ip/neighbor/discovery-settings": {
		"lldp_med": {Min: "7.23"},
	},
}

// rawConfigReader is satisfied by both *schema.ResourceData (apply time) and
// *schema.ResourceDiff (plan time), so one check serves both phases.
type rawConfigReader interface {
	GetRawConfig() cty.Value
}

// unsupportedAttributes returns an explanation for each attribute the
// configuration sets that the running RouterOS does not support. Attributes the
// user did not set are never considered.
func unsupportedAttributes(s map[string]*schema.Schema, d rawConfigReader) []string {
	pathAttr, ok := s[MetaResourcePath]
	if !ok {
		return nil
	}
	bounded, ok := attributeAvailability[pathAttr.Default.(string)]
	if !ok {
		return nil
	}

	// Without a detected version there is nothing to compare against; stay quiet
	// rather than guess.
	if RouterOSVersion == "" {
		return nil
	}
	current, err := parseRouterOSVersion(RouterOSVersion)
	if err != nil {
		return nil
	}

	rawConfig := d.GetRawConfig()
	if rawConfig.IsNull() || !rawConfig.IsKnown() {
		return nil
	}

	var found []string
	for attr, bounds := range bounded {
		if _, inSchema := s[attr]; !inSchema {
			continue
		}
		value := rawConfig.GetAttr(attr)
		if value.IsNull() || !value.IsKnown() {
			continue
		}
		if msg := boundsViolation(attr, bounds, current); msg != "" {
			found = append(found, msg)
		}
	}
	sort.Strings(found)
	return found
}

// boundsViolation reports that `current` falls outside the supported range, or
// returns an empty string when the attribute is supported.
//
// The message states only what the table knows: the attribute, the running
// version, and the range it is valid for. How a given RouterOS build reacts to
// an unsupported property is not consistent -- some are rejected outright with
// "unknown parameter", others are accepted and never reported back -- so the
// error does not claim a particular outcome.
func boundsViolation(attr string, bounds attrVersions, current uint64) string {
	outOfRange := false
	if bounds.Min != "" {
		if min, err := parseRouterOSVersion(bounds.Min); err == nil && current < min {
			outOfRange = true
		}
	}
	if bounds.Max != "" {
		if max, err := parseRouterOSVersion(bounds.Max); err == nil && current > max {
			outOfRange = true
		}
	}
	if !outOfRange {
		return ""
	}

	return fmt.Sprintf("RouterOS %s does not support %q. Supported versions: %s. Remove %q from the configuration.",
		RouterOSVersion, attr, bounds.supportedRange(), attr)
}

// supportedRange renders the bound as "7.15-7.22", "7.23 or newer", or
// "up to 7.22".
func (b attrVersions) supportedRange() string {
	switch {
	case b.Min != "" && b.Max != "":
		return b.Min + "-" + b.Max
	case b.Min != "":
		return b.Min + " or newer"
	default:
		return "up to " + b.Max
	}
}

// CheckAttributeAvailability reports unsupported attributes as diagnostics, for
// the create and update paths. Plan-time reporting is the primary path; this
// remains as a backstop for resources with custom CRUD that bypass the diff.
func CheckAttributeAvailability(s map[string]*schema.Schema, d *schema.ResourceData) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, msg := range unsupportedAttributes(s, d) {
		diags = append(diags, diag.Diagnostic{
			Severity: diag.Error,
			Summary:  "Attribute not supported by this RouterOS version",
			Detail:   msg,
		})
	}
	return diags
}

// versionAvailabilityDiff runs the check during plan, so an unsupported
// attribute is reported before anything is written to the router.
func versionAvailabilityDiff(s map[string]*schema.Schema) schema.CustomizeDiffFunc {
	return func(ctx context.Context, d *schema.ResourceDiff, m interface{}) error {
		msgs := unsupportedAttributes(s, d)
		if len(msgs) == 0 {
			return nil
		}
		return errors.New(strings.Join(msgs, "\n"))
	}
}

// AttachVersionAvailabilityChecks wires the plan-time check into every resource
// that has version-bounded attributes, chaining onto any existing CustomizeDiff
// rather than replacing it.
func AttachVersionAvailabilityChecks(resources map[string]*schema.Resource) {
	for _, res := range resources {
		pathAttr, ok := res.Schema[MetaResourcePath]
		if !ok {
			continue
		}
		if _, bounded := attributeAvailability[pathAttr.Default.(string)]; !bounded {
			continue
		}

		check := versionAvailabilityDiff(res.Schema)
		if existing := res.CustomizeDiff; existing != nil {
			res.CustomizeDiff = func(ctx context.Context, d *schema.ResourceDiff, m interface{}) error {
				if err := check(ctx, d, m); err != nil {
					return err
				}
				return existing(ctx, d, m)
			}
			continue
		}
		res.CustomizeDiff = check
	}
}
