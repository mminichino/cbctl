package rest

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mminichino/cbctl/internal/models"
)

// ParseNodeSpec parses HOST[=SERVICES][@RAM][#ALTERNATE[;PORTMAP]].
func ParseNodeSpec(spec string, defaultServices []string, defaultRAM int) (models.NodeSpec, error) {
	hostPart := spec
	services := append([]string(nil), defaultServices...)
	ram := defaultRAM
	var alternate string
	ports := map[string]int{}

	if i := strings.LastIndex(hostPart, "#"); i >= 0 {
		altText := hostPart[i+1:]
		hostPart = hostPart[:i]
		var err error
		alternate, ports, err = ParseAlternateFragment(altText)
		if err != nil {
			return models.NodeSpec{}, err
		}
	}

	if i := strings.LastIndex(hostPart, "@"); i >= 0 {
		ramText := strings.TrimSpace(hostPart[i+1:])
		hostPart = hostPart[:i]
		if ramText == "" || !isDigits(ramText) {
			return models.NodeSpec{}, fmt.Errorf(
				"invalid RAM in node spec %q; expected HOST[=SERVICES][@RAM][#ALTERNATE]",
				spec,
			)
		}
		var err error
		ram, err = strconv.Atoi(ramText)
		if err != nil {
			return models.NodeSpec{}, err
		}
	}

	if i := strings.Index(hostPart, "="); i >= 0 {
		servicesText := hostPart[i+1:]
		hostPart = hostPart[:i]
		parsed := ParseServices(servicesText, nil)
		if len(parsed) == 0 {
			return models.NodeSpec{}, fmt.Errorf(
				"invalid services in node spec %q; expected HOST[=SERVICES][@RAM][#ALTERNATE]",
				spec,
			)
		}
		services = parsed
	}

	host := strings.TrimSpace(hostPart)
	if host == "" {
		return models.NodeSpec{}, fmt.Errorf(
			"invalid node spec %q; expected HOST[=SERVICES][@RAM][#ALTERNATE]",
			spec,
		)
	}
	return models.NodeSpec{
		Host:             host,
		Services:         services,
		RAMGiB:           ram,
		AlternateAddress: alternate,
		AlternatePorts:   ports,
	}, nil
}

// ParseAlternateFragment parses HOST or HOST;PORTMAP.
func ParseAlternateFragment(fragment string) (string, map[string]int, error) {
	text := strings.TrimSpace(fragment)
	if text == "" {
		return "", map[string]int{}, nil
	}
	if host, portsText, ok := strings.Cut(text, ";"); ok {
		ports, err := ParseAlternatePorts(portsText)
		if err != nil {
			return "", nil, err
		}
		host = strings.TrimSpace(host)
		if host == "" {
			return "", ports, nil
		}
		return host, ports, nil
	}
	return text, map[string]int{}, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
