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
	"github.com/influxdata/telegraf/internal"
	"github.com/influxdata/telegraf/internal/tls"
	"github.com/influxdata/telegraf/plugins/inputs"
)

const (
	defaultResponseTimeout = 5 * time.Second
	apiURI                 = "/zebi/api/v2"
)

// Intelliflash structure
type intelliflash struct {
	Servers  []string
	Username string
	Password string

	ResponseTimeout internal.Duration

	SysMetricsInclude  []string      `toml:"system_metrics_include,omitempty"`
	SysMetricsExclude  []string      `toml:"system_metrics_exclude,omitempty"`
	DataMetricsInclude []dataMetrics `toml:"data_metrics,omitempty"`

	tls.ClientConfig
	client *http.Client
}

type dataMetrics struct {
	DataSets  []string `toml:"datasets,omitempty"`
	Vms       []string `toml:"vms,omitempty"`
	Protocols []string `toml:"protocols,omitempty"`
}

type systemAnalyticsElement struct {
	SystemAnalyticsType string               `json:"systemAnalyticsType"`
	Timestamps          []int64              `json:"timestamps"`
	Datapoints          map[string][]float64 `json:"datapoints"`
	Averages            map[string]float64   `json:"averages"`
}

var sampleConfig = `
  ## Minimum collection interval should be 1 minute. Smaller doesn't make
  ## sense as Intelliflash has a collection interval of 1 minute.
  interval = "1m"

  ## An array of address to gather stats about. Specify an ip on hostname.
  servers = ["localhost","127.0.0.1"]

  ## Credentials for basic HTTP authentication
  username = "admin"
  password = "admin"

  # System metrics to include (if ommited or empty, all metrics are collected)
  system_metrics_include = [
	"NETWORK",
	"POOL_PERFORMANCE",
	"CPU",
	"CACHE_HITS"
  ]
  # system_metrics_exclude = [] ## By default nothing is excluded

  ## Optional TLS Config
  # tls_ca = "/etc/telegraf/ca.pem"
  # tls_cert = "/etc/telegraf/cert.cer"
  # tls_key = "/etc/telegraf/key.key"
  ## Use TLS but skip chain & host verification
  # insecure_skip_verify = false

  # HTTP response timeout (default: 5s)
  # response_timeout = "5s"

  # Data metrics to include (By default no data metrics are collected)
  # [[inputs.intelliflash.data_metrics]]
  #   datasets = ["Pool-A/Project/Dataset", "Pool-B/Project/Dataset"]
  #   vms = ["Pool-A/vm-test", "Pool-B/vm-test"]
  #   protocols = ["nfs", "smb", "iscsi", "fc"]
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
			if len(s.DataMetricsInclude) > 0 {
				if err := s.getOneMinuteDataAnalyticsHistory(serv, acc); err != nil {
					acc.AddError(err)
				}
			}
		}(server)
	}

	wg.Wait()
	return nil
}

func (s *intelliflash) getOneMinuteSystemAnalyticsHistory(addr string, acc telegraf.Accumulator) error {
	var data = []byte(`[["NETWORK", "POOL_PERFORMANCE", "CPU", "CACHE_HITS"]]`)
	if len(s.SysMetricsInclude) > 0 {
		data = []byte(`[["` + strings.Join(s.SysMetricsInclude[:], `","`) + `"]]`)
	}
	URL := "https://" + addr + apiURI + "/getOneMinuteSystemAnalyticsHistory"

	resp, err := s.doRequest(URL, data)
	if err != nil {
		return err
	}

	if err := s.importData(resp.Body, acc, addr); err != nil {
		return fmt.Errorf("Unable to parse stats result from '%s': %s", addr, err)
	}
	return nil
}

func (s *intelliflash) getOneMinuteDataAnalyticsHistory(addr string, acc telegraf.Accumulator) error {
	return nil
}

func (s *intelliflash) importData(r io.Reader, acc telegraf.Accumulator, host string) error {
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

				tags := map[string]string{}

				tags["array"] = host
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

func (s *intelliflash) doRequest(URL string, data []byte) (*http.Response, error) {
	if s.client == nil {
		tlsCfg, err := s.ClientConfig.TLSConfig()
		if err != nil {
			return nil, err
		}
		tr := &http.Transport{
			ResponseHeaderTimeout: time.Duration(3 * time.Second),
			TLSClientConfig:       tlsCfg,
		}
		client := &http.Client{
			Transport: tr,
			Timeout:   time.Duration(s.ResponseTimeout.Duration),
		}
		s.client = client
	}

	u, err := url.Parse(URL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", URL, bytes.NewBuffer(data))
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Content-Type", "application/json")

	addr := u.Hostname()
	if u.User != nil {
		p, _ := u.User.Password()
		req.SetBasicAuth(u.User.Username(), p)
		u.User = &url.Userinfo{}
	}

	if s.Username != "" || s.Password != "" {
		req.SetBasicAuth(s.Username, s.Password)
	} else {
		return nil, fmt.Errorf("Username or password not set")
	}

	res, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Unable to connect to intelliflash API '%s': %s", addr, err)
	}

	if res.StatusCode != 200 {
		return nil, fmt.Errorf("Unable to get valid stat result from '%s', http response code : %d", addr, res.StatusCode)
	}
	return res, nil
}

func init() {
	inputs.Add("intelliflash", func() telegraf.Input {
		return &intelliflash{
			ResponseTimeout:   internal.Duration{Duration: defaultResponseTimeout},
			SysMetricsInclude: nil,
			SysMetricsExclude: nil,
		}
	})
}
