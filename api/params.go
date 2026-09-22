package api

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Params are request parameters for a VK API method. Values are converted
// with Set, which understands the common Go scalar types and slices of them.
type Params map[string]string

// Set stores v under key and returns p for chaining. Booleans become 1/0,
// integers and floats are formatted, fmt.Stringer is honoured, slices are
// joined with commas, nil values are skipped.
func (p Params) Set(key string, v any) Params {
	if s, ok := format(v); ok {
		p[key] = s
	}
	return p
}

func format(v any) (string, bool) {
	switch x := v.(type) {
	case nil:
		return "", false
	case string:
		return x, true
	case bool:
		if x {
			return "1", true
		}
		return "0", true
	case int:
		return strconv.Itoa(x), true
	case int32:
		return strconv.FormatInt(int64(x), 10), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case uint32:
		return strconv.FormatUint(uint64(x), 10), true
	case uint64:
		return strconv.FormatUint(x, 10), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case []string:
		return strings.Join(x, ","), true
	case []int64:
		parts := make([]string, len(x))
		for i, n := range x {
			parts[i] = strconv.FormatInt(n, 10)
		}
		return strings.Join(parts, ","), true
	case []int:
		parts := make([]string, len(x))
		for i, n := range x {
			parts[i] = strconv.Itoa(n)
		}
		return strings.Join(parts, ","), true
	case fmt.Stringer:
		return x.String(), true
	default:
		return fmt.Sprint(x), true
	}
}

// Values converts p into url.Values.
func (p Params) Values() url.Values {
	v := make(url.Values, len(p))
	for k, s := range p {
		v.Set(k, s)
	}
	return v
}
