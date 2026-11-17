package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

// groupExamples are the Examples: of each command group's --help, keyed by the path below the
// root. Every line is checked by the help contract test (real command, real flags).
var groupExamples = map[string][]string{
	"": {
		"cbctl cluster create --host 127.0.0.1 -u Administrator -p password --no-ssl",
		"cbctl keyspace create orders.inventory.items --host 127.0.0.1 -p password",
		"cbctl capella guide getting-started",
		"cbctl completion zsh",
	},
	"cluster": {
		"cbctl cluster create --host 127.0.0.1 -u Administrator -p password --no-ssl",
		"cbctl cluster join --rally-ip-address 10.0.0.1 --ip-address 10.0.0.2 -p password",
		"cbctl cluster rebalance --rally-ip-address 10.0.0.1 -p password",
		"cbctl cluster test --host 127.0.0.1 -p password",
	},
	"bucket": {
		"cbctl bucket create orders --host 127.0.0.1 -p password",
		"cbctl bucket exists orders --host 127.0.0.1 -p password",
	},
	"scope": {
		"cbctl scope create inventory --bucket orders --host 127.0.0.1 -p password",
		"cbctl scope exists inventory --bucket orders --host 127.0.0.1 -p password",
	},
	"collection": {
		"cbctl collection create items --bucket orders --scope inventory --host 127.0.0.1 -p password",
		"cbctl collection exists items --bucket orders --scope inventory --host 127.0.0.1 -p password",
	},
	"keyspace": {
		"cbctl keyspace create orders.inventory.items --host 127.0.0.1 -p password",
	},
	"completion": {
		"cbctl completion bash > /etc/bash_completion.d/cbctl",
		"cbctl completion zsh > \"${fpath[1]}/_cbctl\"",
		"cbctl completion fish > ~/.config/fish/completions/cbctl.fish",
	},
	"configure": {
		"cbctl configure --profile dev --org-id ORG_ID",
		"cbctl configure show",
		"cbctl configure migrate --dry-run",
	},
	"capella": {
		"cbctl capella init",
		"cbctl capella plan",
		"cbctl capella apply --yes",
		"cbctl capella cluster list",
		"cbctl capella guide",
	},
	"capella configure": {
		"cbctl capella configure --profile dev --org-id ORG_ID",
		"cbctl capella configure list",
	},
	"capella project": {
		"cbctl capella project list",
		"cbctl capella project create nova-dev --description \"Nova development\"",
		"cbctl capella project delete nova-dev --cascade -y",
	},
	"capella cluster": {
		"cbctl capella cluster list",
		"cbctl capella cluster get nova-dev-01",
		"cbctl capella cluster create --name nova-dev-01 --size small",
		"cbctl capella cluster scale nova-dev-01 --step +1",
		"cbctl capella cluster power nova-dev-01 --off -y",
	},
	"capella cluster group": {
		"cbctl capella cluster group list nova-dev-01",
		"cbctl capella cluster group add nova-dev-01 --services search --nodes 2",
		"cbctl capella cluster group remove nova-dev-01 search -y",
	},
	"capella cluster schedule": {
		"cbctl capella cluster schedule show nova-dev-01",
		"cbctl capella cluster schedule set nova-dev-01 --timezone US/Pacific --weekdays 08:00-18:00 --weekend off",
		"cbctl capella cluster schedule pause nova-dev-01",
	},
	"capella cluster backup": {
		"cbctl capella cluster backup list nova-dev-01",
		"cbctl capella cluster backup create nova-dev-01 --retain 7d",
		"cbctl capella cluster backup schedule show nova-dev-01",
	},
	"capella cluster backup schedule": {
		"cbctl capella cluster backup schedule show nova-dev-01",
		"cbctl capella cluster backup schedule set nova-dev-01 --every 24 --retain 30d",
	},
	"capella bucket": {
		"cbctl capella bucket list nova-dev-01",
		"cbctl capella bucket add nova-dev-01 orders --memory 1024",
		"cbctl capella bucket update nova-dev-01 orders --replicas 2",
	},
	"capella bucket backup": {
		"cbctl capella bucket backup list nova-dev-01",
		"cbctl capella bucket backup create nova-dev-01 orders",
		"cbctl capella bucket backup schedule show nova-dev-01 orders",
	},
	"capella bucket backup schedule": {
		"cbctl capella bucket backup schedule show nova-dev-01 orders",
		"cbctl capella bucket backup schedule set nova-dev-01 orders --day sunday --retention 30days",
	},
	"capella bucket backup cycle": {
		"cbctl capella bucket backup cycle list nova-dev-01 orders",
		"cbctl capella bucket backup cycle get nova-dev-01 orders CYCLE_ID",
	},
	"capella scope": {
		"cbctl capella scope list nova-dev-01 orders",
		"cbctl capella scope add nova-dev-01 orders inventory",
	},
	"capella collection": {
		"cbctl capella collection list nova-dev-01 orders inventory",
		"cbctl capella collection add nova-dev-01 orders inventory items --max-ttl 3600",
	},
	"capella cidr": {
		"cbctl capella cidr list nova-dev-01",
		"cbctl capella cidr create nova-dev-01 203.0.113.10/32 --comment \"Office VPN\"",
	},
	"capella credential": {
		"cbctl capella credential list nova-dev-01",
		"cbctl capella credential create nova-dev-01 orders_app --privileges read+write --bucket orders",
	},
}

// installGroupExamples gives every command group without an Example the one from groupExamples.
func installGroupExamples(root *cobra.Command) {
	var walk func(c *cobra.Command, path string)
	walk = func(c *cobra.Command, path string) {
		if lines, ok := groupExamples[path]; ok && c.Example == "" {
			c.Example = "  " + strings.Join(lines, "\n  ")
		}
		for _, sub := range c.Commands() {
			p := sub.Name()
			if path != "" {
				p = path + " " + p
			}
			walk(sub, p)
		}
	}
	walk(root, "")
}
