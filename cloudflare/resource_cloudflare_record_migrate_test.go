package cloudflare

import (
	"fmt"
	log "github.com/sourcegraph-ce/logrus"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cloudflare/cloudflare-go"
	"github.com/hashicorp/terraform-plugin-sdk/terraform"
)

func TestCloudflareRecordMigrateState(t *testing.T) {
	// create the test server for mocking the API calls
	ts := mockCloudflareEnv()
	defer ts.Close()

	// Create a Cloudflare client, overriding the BaseURL
	cfMeta, err := cloudflare.New(
		"sometoken",
		"someemail",
		mockHTTPClient(ts.URL),
	)

	if err != nil {
		t.Fatalf("Error building Cloudflare API: %s", err)
	}

	// When several records are returned for a single DNS query, they are
	// matched on ttl, proxied and priority: as the same response is returned
	// for all testcases, select specific values for each testcase
	cases := map[string]struct {
		StateVersion int
		ID           string
		Attributes   map[string]string
		Expected     string
		ShouldFail   bool
	}{
		"ttl_120": {
			StateVersion: 0,
			ID:           "123456",
			Attributes: map[string]string{
				"id":       "123456",
				"name":     "notthesub",
				"hostname": "notthesub.hashicorptest.com",
				"type":     "A",
				"content":  "10.0.2.5",
				"ttl":      "120",
				"zone_id":  "1234567890",
				"domain":   "hashicorptest.com",
			},
			Expected: "7778f8766e583af8de0abfcd76c5dAAA",
		},
		"ttl_121": {
			StateVersion: 0,
			ID:           "123456",
			Attributes: map[string]string{
				"id":       "123456",
				"name":     "notthesub",
				"hostname": "notthesub.hashicorptest.com",
				"type":     "A",
				"content":  "10.0.2.5",
				"ttl":      "121",
				"zone_id":  "1234567890",
				"domain":   "hashicorptest.com",
			},
			Expected: "5558f8766e583af8de0abfcd76c5dBBB",
		},
		"mx_priority": {
			StateVersion: 0,
			ID:           "123456",
			Attributes: map[string]string{
				"id":       "123456",
				"name":     "hashicorptest.com",
				"type":     "MX",
				"content":  "some.registrar-servers.com",
				"ttl":      "1",
				"priority": "20",
				"zone_id":  "1234567890",
				"domain":   "hashicorptest.com",
			},
			Expected: "12342092cbc4c391be33ce548713bba3",
		},
		"mx_priority_mismatch": {
			StateVersion: 0,
			ID:           "123456",
			Attributes: map[string]string{
				"id":       "123456",
				"type":     "MX",
				"name":     "hashicorptest.com",
				"content":  "some.registrar-servers.com",
				"ttl":      "1",
				"priority": "10",
				"zone_id":  "1234567890",
				"domain":   "hashicorptest.com",
			},
			Expected:   "12342092cbc4c391be33ce548713bba3",
			ShouldFail: true,
		},
		"proxied": {
			StateVersion: 0,
			ID:           "123456",
			Attributes: map[string]string{
				"id":       "123456",
				"name":     "tftestingsubv616",
				"hostname": "tftestingsubv616.hashicorptest.com",
				"type":     "A",
				"content":  "52.39.212.111",
				"proxied":  "true",
				"ttl":      "1",
				"zone_id":  "1234567890",
				"domain":   "hashicorptest.com",
			},
			Expected: "888ffe3f93a31231ad6b0c6d09185eee",
		},
		"not_proxied": {
			StateVersion: 0,
			ID:           "123456",
			Attributes: map[string]string{
				"id":       "123456",
				"name":     "tftestingsubv616",
				"hostname": "tftestingsubv616.hashicorptest.com",
				"type":     "A",
				"content":  "52.39.212.111",
				"proxied":  "false",
				"ttl":      "1",
				"zone_id":  "1234567890",
				"domain":   "hashicorptest.com",
			},
			Expected: "222ffe3f93a31231ad6b0c6d09185jjj",
		},
		"ttl_122_state_v0_with_v1_fields": {
			StateVersion: 0,
			ID:           "12345678901234567890123456789012",
			Attributes: map[string]string{
				"created_on":                      "2018-03-07T11:52:02.454564Z",
				"data.%":                          "0",
				"hostname":                        "hashicorptest.com",
				"id":                              "12345678901234567890123456789012",
				"metadata.%":                      "3",
				"metadata.auto_added":             "false",
				"metadata.managed_by_apps":        "false",
				"metadata.managed_by_argo_tunnel": "false",
				"modified_on":                     "2018-03-07T11:52:02.454564Z",
				"name":                            "hashicorptest.com",
				"priority":                        "0",
				"proxiable":                       "true",
				"proxied":                         "true",
				"ttl":                             "122",
				"type":                            "A",
				"value":                           "1.2.3.4",
				"zone_id":                         "1234567890",
			},
			Expected: "12345678901234567890123456789012",
		},
	}

	for tn, tc := range cases {
		is := &terraform.InstanceState{
			ID:         tc.ID,
			Attributes: tc.Attributes,
		}
		is, err := resourceCloudflareRecordMigrateState(
			tc.StateVersion, is, cfMeta)

		if err != nil {
			if tc.ShouldFail {
				// expected error
				continue
			}
			t.Fatalf("bad: %s, err: %#v", tn, err)
		}

		if is.ID != tc.Expected {
			t.Fatalf("bad record id: %s\n\n expected: %s", is.ID, tc.Expected)
		}
	}
}

// cloudflareEnv establishes a httptest server to mock out the Cloudflare API
// endpoints that we'll be calling.
func mockCloudflareEnv() *httptest.Server {
	endpoints := mockEndpoints()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		log.Printf("[DEBUG] Mocker server received request to %q", r.RequestURI)
		rBase, err := url.ParseRequestURI(r.RequestURI)
		if err != nil {
			log.Fatalf("Failed to find the base path: %s", err)
		}
		for _, e := range endpoints {
			if rBase.Path == e.BasePath {
				fmt.Fprintln(w, e.Body)
				w.WriteHeader(200)
				return
			}
		}
		w.WriteHeader(400)
	}))

	return ts
}

// Stub out the two Cloudflare API routes that will be called
func mockEndpoints() []*endpoint {
	return []*endpoint{
		&endpoint{
			BasePath: "/zones",
			Body:     zoneResponse,
		},
		&endpoint{
			BasePath: "/zones/1234567890/dns_records",
			Body:     dnsResponse,
		},
	}
}

type routes struct {
	Endpoints []*endpoint
}
type endpoint struct {
	BasePath string
	Body     string
}

// HTTPClient accepts a custom *http.Client for making API calls.
// This function is used as a callback of sorts to override any of the client
// options that you can't directly set on the struct
func mockHTTPClient(testURL string) cloudflare.Option {
	return func(api *cloudflare.API) error {
		api.BaseURL = testURL
		return nil
	}
}

const zoneResponse = `
{
  "result": [
    {
      "id": "1234567890",
      "name": "hashicorptest.com",
      "status": "active",
      "paused": false,
      "type": "full",
      "development_mode": 0
    }
  ],
  "result_info": {
    "page": 1,
    "per_page": 20,
    "total_pages": 1,
    "count": 1,
    "total_count": 1
  },
  "success": true,
  "errors": [],
  "messages": []
}
`

const dnsResponse = `
{
  "result": [
    {
      "id": "7778f8766e583af8de0abfcd76c5dAAA",
      "type": "A",
      "name": "notthesub.hashicorptest.com",
      "content": "10.0.2.5",
      "proxiable": false,
      "proxied": false,
      "ttl": 120,
      "locked": false,
      "zone_id": "1234567890",
      "zone_name": "hashicorptest.com"
    },
    {
      "id": "5558f8766e583af8de0abfcd76c5dBBB",
      "type": "A",
      "name": "notthesub.hashicorptest.com",
      "content": "10.0.2.5",
      "proxiable": false,
      "proxied": false,
      "ttl": 121,
      "locked": false,
      "zone_id": "1234567890",
      "zone_name": "hashicorptest.com"
    },
    {
      "id": "2220a9593ab869199b65c89bddf72ddd",
      "type": "A",
      "name": "maybethesub.hashicorptest.com",
      "content": "10.0.3.5",
      "proxiable": false,
      "proxied": false,
      "ttl": 120,
      "locked": false,
      "zone_id": "1234567890",
      "zone_name": "hashicorptest.com"
    },
    {
      "id": "222ffe3f93a31231ad6b0c6d09185jjj",
      "type": "A",
      "name": "tftestingsubv616.hashicorptest.com",
      "content": "52.39.212.111",
      "proxiable": true,
      "proxied": false,
      "ttl": 1,
      "locked": false,
      "zone_id": "1234567890",
      "zone_name": "hashicorptest.com"
    },
    {
      "id": "888ffe3f93a31231ad6b0c6d09185eee",
      "type": "A",
      "name": "tftestingsubv616.hashicorptest.com",
      "content": "52.39.212.111",
      "proxiable": true,
      "proxied": true,
      "ttl": 1,
      "locked": false,
      "zone_id": "1234567890",
      "zone_name": "hashicorptest.com"
    },
    {
      "id": "98y6t9ba87e6ee3e6aeba8f3dc52c81b",
      "type": "CNAME",
      "name": "somecname.hashicorptest.com",
      "content": "some.us-west-2.elb.amazonaws.com",
      "proxiable": true,
      "proxied": false,
      "ttl": 120,
      "locked": false,
      "zone_id": "1234567890",
      "zone_name": "hashicorptest.com"
    },
    {
      "id": "12342092cbc4c391be33ce548713bba3",
      "type": "MX",
      "name": "hashicorptest.com",
      "content": "some.registrar-servers.com",
      "proxiable": false,
      "proxied": false,
      "ttl": 1,
      "priority": 20,
      "locked": false,
      "zone_id": "1234567890",
      "zone_name": "hashicorptest.com"
    },
    {
      "id": "12345678901234567890123456789012",
      "name": "hashicorptest.com",
	  "content": "1.2.3.4",
      "proxiable": true,
      "proxied": true,
      "ttl": 122,
      "locked": false,
      "zone_id": "1234567890",
      "zone_name": "hashicorptest.com",
      "modified_on": "2018-03-07T11:52:02.454564Z",
      "created_on": "2018-03-07T11:52:02.454564Z",
      "meta": {
        "auto_added": false,
        "managed_by_apps": false,
	    "managed_by_argo_tunnel": false
	  }
	}
  ],
  "result_info": {
    "page": 1,
    "per_page": 20,
    "total_pages": 1,
    "count": 8,
    "total_count": 8
  },
  "success": true,
  "errors": [],
  "messages": []
}`
