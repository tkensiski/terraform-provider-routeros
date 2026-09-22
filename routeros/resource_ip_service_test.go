package routeros

import (
	"testing"

	"github.com/hashicorp/go-cty/cty"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const testIpServiceAddress = "routeros_ip_service.telnet"

// The services RouterOS reports as non-dynamic rows of /ip/service, measured on
// CHR 7.24.2. A name absent from the schema's accepted list cannot be managed at
// all -- validation rejects it before the provider issues any request -- so this
// list is what keeps the validator level with the firmware.
var testIpServiceNames = []string{
	"api", "api-ssl", "ftp", "reverse-proxy", "ssh", "telnet", "winbox", "www", "www-ssl",
}

func Test_resourceIpServiceNumbersValidation(t *testing.T) {
	validate := ResourceIpService().Schema["numbers"].ValidateDiagFunc
	if validate == nil {
		t.Fatal("the numbers attribute has no ValidateDiagFunc")
	}

	for _, name := range testIpServiceNames {
		t.Run("accepts "+name, func(t *testing.T) {
			if diags := validate(name, *new(cty.Path)); len(diags) != 0 {
				t.Errorf("%q should be accepted, got: %v", name, diags)
			}
		})
	}

	// "dhcp" is a real /ip/service row on 7.24.2, but a dynamic one, so it
	// cannot be configured and must stay rejected. "reverse_proxy" is the
	// underscore spelling Terraform users reach for by habit.
	for _, name := range []string{"dhcp", "reverse_proxy", "unknown-service", ""} {
		t.Run("rejects "+name, func(t *testing.T) {
			if diags := validate(name, *new(cty.Path)); len(diags) == 0 {
				t.Errorf("%q should be rejected, got no diagnostics", name)
			}
		})
	}
}

func TestAccIpServiceTest_basic(t *testing.T) {
	for _, name := range testNames {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck: func() {
					testAccPreCheck(t)
					testSetTransportEnv(t, name)
				},
				ProviderFactories: testAccProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: testAccIpServiceConfig(),
						Check: resource.ComposeTestCheckFunc(
							testResourcePrimaryInstanceId(testIpServiceAddress),
							resource.TestCheckResourceAttr(testIpServiceAddress, "name", "telnet"),
						),
					},
				},
			})
		})
	}
}

func testAccIpServiceConfig() string {
	return providerConfig + `

resource "routeros_ip_service" "telnet" {
	numbers  = "telnet"
	disabled = true
	port     = 23
}
`
}
