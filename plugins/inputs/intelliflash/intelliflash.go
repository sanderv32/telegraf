package intelliflash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/internal/tls"
	"github.com/influxdata/telegraf/plugins/inputs"
)

// Intelliflash structure
type intelliflash struct {
	Servers  []string
	Username string
	Password string

	tls.ClientConfig
	client *http.Client
}

var sampleConfig = `
  ## An array of address to gather stats about. Specify an ip on hostname.
  servers = ["localhost","127.0.0.1"]

  ## Credentials for basic HTTP authentication
  username = "admin"
  password = "admin"

  ## Use TLS but skip chain & host verification
  # insecure_skip_verify = false
`

// SampleConfig func
func (s *intelliflash) SampleConfig() string {
	return sampleConfig
}

// Description func
func (s *intelliflash) Description() string {
	return "Read metrics from Intelliflash"
}

// Gather func
func (s *intelliflash) Gather(acc telegraf.Accumulator) error {
	if len(s.Servers) == 0 {
		return fmt.Errorf("no servers specified")
	}

	endpoints := make([]string, 0, len(s.Servers))
	for _, endpoint := range s.Servers {
		endpoints = append(endpoints, endpoint)
	}

	var wg sync.WaitGroup
	wg.Add(len(endpoints))
	for _, server := range endpoints {
		go func(serv string) {
			defer wg.Done()
			if err := s.getOneMinuteSystemAnalyticsHistory(serv, acc); err != nil {
				acc.AddError(err)
			}
		}(server)
	}

	wg.Wait()
	return nil
}

func (s *intelliflash) getOneMinuteSystemAnalyticsHistory(addr string, acc telegraf.Accumulator) error {
	if s.client == nil {
		tlsCfg, err := s.ClientConfig.TLSConfig()
		if err != nil {
			return err
		}
		tr := &http.Transport{
			ResponseHeaderTimeout: time.Duration(3 * time.Second),
			TLSClientConfig:       tlsCfg,
		}
		client := &http.Client{
			Transport: tr,
			Timeout:   time.Duration(4 * time.Second),
		}
		s.client = client
	}

	u, err := url.Parse(addr)
	if err != nil {
		return fmt.Errorf("Unable parse server address '%s': %s", addr, err)
	}

	var data = []byte(`[["NETWORK", "POOL_PERFORMANCE", "CPU", "CACHE_HITS"]]`)

	URL := "https://" + addr + "/zebi/api/v2/getOneMinuteSystemAnalyticsHistory"

	req, err := http.NewRequest("POST", URL, bytes.NewBuffer(data))
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Content-Type", "application/json")

	if u.User != nil {
		p, _ := u.User.Password()
		req.SetBasicAuth(u.User.Username(), p)
		u.User = &url.Userinfo{}
		addr = u.String()
	}

	if s.Username != "" || s.Password != "" {
		req.SetBasicAuth(s.Username, s.Password)
	}

	res, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("Unable to connect to intelliflash server '%s': %s", addr, err)
	}

	if res.StatusCode != 200 {
		return fmt.Errorf("Unable to get valid stat result from '%s', http response code : %d", addr, res.StatusCode)
	}

	if err := s.importData(res.Body, acc, u.Host); err != nil {
		return fmt.Errorf("Unable to parse stat result from '%s': %s", addr, err)
	}
	return nil
}

var analyticTypes = []string{"CPU", "NETWORK", "CACHE_HITS", "POOL_PERFORMANCE"}

type systemAnalyticsElement struct {
	SystemAnalyticsType string               `json:"systemAnalyticsType"`
	Timestamps          []int64              `json:"timestamps"`
	Datapoints          map[string][]float64 `json:"datapoints"`
	Averages            map[string]float64   `json:"averages"`
}

func (s *intelliflash) importData(r io.Reader, acc telegraf.Accumulator, host string) error {
	var tags map[string]string
	var systemAnalytics []systemAnalyticsElement

	resp, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	err = json.Unmarshal(resp, &systemAnalytics)
	if err != nil {
		return fmt.Errorf("Error decoding JSON")
	}

	for idx := range systemAnalytics {
		for dpname, datapoint := range systemAnalytics[idx].Datapoints {
			for midx := range datapoint {
				fields := make(map[string]interface{})

				tags = map[string]string{
					"server": host,
				}

				name := strings.Split(dpname, "/")
				switch systemAnalytics[idx].SystemAnalyticsType {
				case "POOL_PERFORMANCE":
					tags["pool"] = name[0]
					tags["disktype"] = name[1]
					fields[name[2]] = datapoint[midx]
				case "NETWORK":
					tags["controller"] = name[0]
					if strings.HasPrefix(name[1], "I") {
						// Interface[Group] metrics
						tags["interface"] = name[2]
						fields[name[3]] = datapoint[midx]
					} else {
						// Controller totals
						fields[name[1]+"_"+name[2]] = datapoint[midx]
					}
				case "CPU":
					tags["controller"] = name[0]
					fields[name[1]] = datapoint[midx]
				case "CACHE_HITS":
					tags["controller"] = name[0]
					fields[name[1]] = datapoint[midx]

				}
				// fmt.Println(dpname, datapoint, tags, fields, systemAnalytics[idx].Timestamps[midx])
				acc.AddFields("intelliflash", fields, tags, time.Unix(systemAnalytics[idx].Timestamps[midx]/1000, 0))
			}

		}
	}
	return nil
}

func init() {
	inputs.Add("intelliflash", func() telegraf.Input {
		return &intelliflash{}
	})
}
