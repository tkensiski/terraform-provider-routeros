package routeros

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const testIpServiceAddress = "routeros_ip_service.telnet"

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
					{
						// Import by service name (IdType=Name). Verifies the state
						// round-trips with no drift — in particular that the Required
						// "numbers" selector is seeded from the import ID (the API only
						// returns "name", never "numbers").
						ResourceName:      testIpServiceAddress,
						ImportState:       true,
						ImportStateId:     "telnet",
						ImportStateVerify: true,
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
