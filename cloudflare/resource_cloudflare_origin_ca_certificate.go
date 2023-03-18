package cloudflare

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	log "github.com/sourcegraph-ce/logrus"
	"strings"
	"time"

	cloudflare "github.com/cloudflare/cloudflare-go"
	"github.com/hashicorp/terraform-plugin-sdk/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/helper/validation"
)

func resourceCloudflareOriginCACertificate() *schema.Resource {
	return &schema.Resource{
		Create: resourceCloudflareOriginCACertificateCreate,
		Read:   resourceCloudflareOriginCACertificateRead,
		Delete: resourceCloudflareOriginCACertificateDelete,
		Importer: &schema.ResourceImporter{
			State: schema.ImportStatePassthrough,
		},

		Schema: map[string]*schema.Schema{
			"certificate": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"csr": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validateCSR,
			},
			"expires_on": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"hostnames": {
				Type:     schema.TypeSet,
				Required: true,
				ForceNew: true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
			},
			"request_type": {
				Type:         schema.TypeString,
				Required:     true,
				ForceNew:     true,
				ValidateFunc: validation.StringInSlice([]string{"origin-rsa", "origin-ecc", "keyless-certificate"}, false),
			},
			"requested_validity": {
				Type:         schema.TypeInt,
				Optional:     true,
				ForceNew:     true,
				ValidateFunc: validation.IntInSlice([]int{7, 30, 90, 365, 730, 1095, 5475}),
			},
		},
	}
}

func resourceCloudflareOriginCACertificateCreate(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*cloudflare.API)

	hostnames := []string{}
	hostnamesRaw := d.Get("hostnames").(*schema.Set)
	for _, h := range hostnamesRaw.List() {
		hostnames = append(hostnames, h.(string))
	}

	certInput := cloudflare.OriginCACertificate{
		CSR:         d.Get("csr").(string),
		Hostnames:   hostnames,
		RequestType: d.Get("request_type").(string),
	}

	requestValidity, ok := d.GetOk("requested_validity")
	if ok {
		certInput.RequestValidity = requestValidity.(int)
	}

	log.Printf("[INFO] Creating Cloudflare OriginCACertificate: hostnames %v", hostnames)
	cert, err := client.CreateOriginCertificate(certInput)

	if err != nil {
		return fmt.Errorf("Error creating origin certificate: %s", err)
	}

	d.SetId(cert.ID)
	d.Set("certificate", cert.Certificate)
	d.Set("expires_on", cert.ExpiresOn.Format(time.RFC3339))
	return nil
}

func resourceCloudflareOriginCACertificateRead(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*cloudflare.API)
	certID := d.Id()
	cert, err := client.OriginCertificate(certID)

	log.Printf("[DEBUG] OriginCACertificate: %#v", cert)

	if err != nil {
		if strings.Contains(err.Error(), "Failed to read certificate from Database") {
			log.Printf("[INFO] OriginCACertificate %s does not exist", certID)
			d.SetId("")
			return nil
		}
		return fmt.Errorf("Error finding OriginCACertificate %q: %s", certID, err)
	}

	if cert.RevokedAt != (time.Time{}) {
		log.Printf("[INFO] OriginCACertificate %s has been revoked", certID)
		d.SetId("")
		return nil
	}

	hostnames := schema.NewSet(schema.HashString, []interface{}{})
	for _, h := range cert.Hostnames {
		hostnames.Add(h)
	}

	d.Set("certificate", cert.Certificate)
	d.Set("expires_on", cert.ExpiresOn.Format(time.RFC3339))
	d.Set("hostnames", hostnames)
	d.Set("request_type", cert.RequestType)

	return nil
}

func resourceCloudflareOriginCACertificateDelete(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*cloudflare.API)
	certID := d.Id()

	log.Printf("[INFO] Revoking Cloudflare OriginCACertificate: id %s", certID)

	_, err := client.RevokeOriginCertificate(certID)

	if err != nil {
		return fmt.Errorf("Error revoking Cloudflare OriginCACertificate: %s", err)
	}

	d.SetId("")
	return nil
}

func validateCSR(v interface{}, k string) (ws []string, errors []error) {
	block, _ := pem.Decode([]byte(v.(string)))
	if block == nil {
		errors = append(errors, fmt.Errorf("%q: invalid PEM data", k))
		return
	}

	_, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %s", k, err.Error()))
	}
	return
}
