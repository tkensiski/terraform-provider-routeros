package routeros

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

/*
  {
    ".id": "*0",
    "address": "",
    "disabled": "false",
    "invalid": "false",
    "name": "telnet",
    "port": "23",
    "vrf": "main"
  },
  {
    ".id": "*6",
    "address": "",
    "certificate": "https-cert",
    "disabled": "false",
    "invalid": "false",
    "name": "www-ssl",
    "port": "443",
    "tls-version": "any",
    "vrf": "main"
  },
*/

// https://help.mikrotik.com/docs/display/ROS/Services
func ResourceIpService() *schema.Resource {
	resSchema := map[string]*schema.Schema{
		MetaResourcePath: PropResourcePath("/ip/service"),
		MetaId:           PropId(Name),

		"address": {
			Type:        schema.TypeString,
			Optional:    true,
			Default:     "",
			Description: "List of IP/IPv6 prefixes from which the service is accessible.",
			DiffSuppressFunc: func(k, oldValue, newValue string, d *schema.ResourceData) bool {
				if oldValue == "" && newValue == "0.0.0.0/0" {
					return false
				}
				return oldValue == newValue
			},
		},
		"certificate": {
			Type:     schema.TypeString,
			Optional: true,
			Description: "The name of the certificate used by a particular service. Applicable only for services " +
				"that depend on certificates ( www-ssl, api-ssl ).",
			DiffSuppressFunc: AlwaysPresentNotUserProvided,
		},
		KeyDisabled: PropDisabledRw,
		KeyDynamic:  PropDynamicRo,
		KeyInvalid:  PropInvalidRo,
		"max_sessions": {
			Type:             schema.TypeInt,
			Optional:         true,
			Description:      "Maximum number of concurrent connections to a particular service. This option is available in RouterOS starting from version 7.16.",
			ValidateFunc:     validation.IntAtLeast(1),
			DiffSuppressFunc: AlwaysPresentNotUserProvided,
		},
		"name": {
			Type:        schema.TypeString,
			Computed:    true,
			Description: "Service name.",
		},
		"numbers": {
			Type:     schema.TypeString,
			Required: true,
			Description: "The name of the service whose settings will be changed ( api, api-ssl, ftp, ssh, telnet, " +
				"winbox, www, www-ssl ).",
			ValidateDiagFunc: ValidationMultiValInSlice([]string{"api", "api-ssl", "ftp", "ssh", "telnet", "winbox",
				"www", "www-ssl"}, false, false),
		},
		"port": {
			Type:         schema.TypeInt,
			Required:     true,
			Description:  "The port particular service listens on.",
			ValidateFunc: validation.IntBetween(1, 65535),
		},
		"proto": {
			Type:     schema.TypeString,
			Computed: true,
		},
		"tls_version": {
			Type:             schema.TypeString,
			Optional:         true,
			Description:      "Specifies which TLS versions to allow by a particular service.",
			ValidateFunc:     validation.StringInSlice([]string{"any", "only-1.2"}, false),
			DiffSuppressFunc: AlwaysPresentNotUserProvided,
		},
		KeyVrf: PropVrfRw,
	}

	resCreateUpdate := func(ctx context.Context, d *schema.ResourceData, m interface{}) diag.Diagnostics {
		item, metadata := TerraformResourceDataToMikrotik(resSchema, d)

		d.SetId(d.Get("numbers").(string))

		var resUrl string
		if m.(Client).GetTransport() == TransportREST {
			// https://router/rest/system/identity/set
			// https://router/rest/caps-man/manager/set
			resUrl = "/set"
		}

		err := m.(Client).SendRequest(crudPost, &URL{Path: metadata.Path + resUrl}, item, nil)
		if err != nil {
			return diag.FromErr(err)
		}

		return ResourceRead(ctx, resSchema, d, m)
	}

	return &schema.Resource{
		CreateContext: resCreateUpdate,
		ReadContext:   DefaultRead(resSchema),
		UpdateContext: resCreateUpdate,
		DeleteContext: DefaultSystemDelete(resSchema),

		// ip_service is a fixed, name-addressed menu (IdType=Name): Create/Update/Read all
		// key on the service name, and the API returns "name" — never the schema's Required
		// "numbers" selector. Import by the service name (as the docs show:
		// `terraform import '...["www-ssl"]' www-ssl`) and seed "numbers" from it so the
		// post-import read is drift-free. Plain passthrough would leave "numbers" empty
		// (the read never sets it), and the generic ImportStateCustomContext would instead
		// store the internal ".id" (e.g. *6), which the name-keyed read can't find.
		Importer: &schema.ResourceImporter{
			StateContext: func(ctx context.Context, d *schema.ResourceData, m interface{}) ([]*schema.ResourceData, error) {
				// Import by service name, e.g. `www-ssl`. The provider-wide
				// `field=value` form is also accepted for the name/numbers fields
				// (e.g. `name=www-ssl`).
				id := d.Id()
				if k, v, found := strings.Cut(id, "="); found && (k == "name" || k == "numbers") {
					id = v
					d.SetId(id)
				}
				// Seed the Required "numbers" selector from the ID: the API returns
				// "name" but never "numbers", so without this the imported resource
				// would show a permanent diff on "numbers".
				if err := d.Set("numbers", id); err != nil {
					return nil, err
				}
				return []*schema.ResourceData{d}, nil
			},
		},

		Schema: resSchema,
	}
}
