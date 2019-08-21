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

type analyticsType int

const (
	defaultResponseTimeout = 10 * time.Second
	apiURI                 = "/zebi/api/v2"
	// SYSTEM enum
	SYSTEM analyticsType = 0
	// DATA enum
	DATA analyticsType = 1
)

// Intelliflash structure
type intelliflash struct {
	Servers  []string
	Username string
	Password string

	ResponseTimeout internal.Duration

	SysMetrics  []string      `toml:"system_metrics_include,omitempty"`
	DataMetrics []dataMetrics `toml:"data_metrics,omitempty"`

	tls.ClientConfig
	client *http.Client
	Debug  bool

	results chan *http.Response
}

type workerResponse struct {
	httpResponse *http.Response
	err          error
}

type dataMetrics struct {
	DataSets  []string `toml:"datasets,omitempty"`
	Vms       []string `toml:"vms,omitempty"`
	Protocols []string `toml:"protocols,omitempty"`
}

type analyticsElement struct {
	SystemAnalyticsType string               `json:"systemAnalyticsType"`
	EntityType          string               `json:"entityType"`
	EntityName          string               `json:"entityName"`
	Timestamps          []int64              `json:"timestamps"`
	Datapoints          map[string][]float64 `json:"datapoints"`
	Averages            map[string]float64   `json:"averages"`
}

type zebiException struct {
	Code         string       `json:"code"`
	Details      string       `json:"details"`
	Message      string       `json:"message"`
	ExtendedData extendedData `json:"extendedData"`
}

type extendedData struct {
	ExCauseMessage string `json:"EX_CAUSE_MESSAGE"`
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
			if err := s.getOneMinuteAnalyticsHistory(serv, acc, SYSTEM); err != nil {
				acc.AddError(err)
			}
			if len(s.DataMetrics) > 0 {
				if err := s.getOneMinuteAnalyticsHistory(serv, acc, DATA); err != nil {
					acc.AddError(err)
				}
			}
		}(server)
	}

	wg.Wait()
	return nil
}

func (s *intelliflash) getOneMinuteAnalyticsHistory(addr string, acc telegraf.Accumulator, t analyticsType) error {
	var URL string
	var data []byte

	jobs := make(chan []byte, 100)
	results := make(chan workerResponse, 100)

	switch t {
	case SYSTEM:
		URL = "https://" + addr + apiURI + "/getOneMinuteSystemAnalyticsHistory"
		data = []byte(`[["NETWORK", "POOL_PERFORMANCE", "CPU", "CACHE_HITS"]]`)
		if len(s.SysMetrics) > 0 {
			data = []byte(`[["` + strings.Join(s.SysMetrics[:], `","`) + `"]]`)
		}

		jobs <- data
	case DATA:
		URL = "https://" + addr + apiURI + "/getOneMinuteDataAnalyticsHistory"
		for _, datametric := range s.DataMetrics {
			jsonreq := fmt.Sprintf("[%s,%s,%s]",
				emptyThenNull(strings.Join(datametric.DataSets[:], `","`)),
				emptyThenNull(strings.Join(datametric.Vms[:], `","`)),
				emptyThenNull(strings.ToUpper(strings.Join(datametric.Protocols[:], `","`))),
			)
			data = []byte(jsonreq)
			jobs <- data
		}
	default:
		return fmt.Errorf("Unknown analytics type")
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.doRequest(URL, jobs, results)
	}()
	wg.Wait()

	for {
		result, ok := <-results
		if ok == false {
			break
		}
		if result.err != nil {
			return fmt.Errorf("Unable to parse stats result from '%s': %s", addr, result.err)
		}
		if err := s.importData(result.httpResponse.Body, acc, addr, t); err != nil {
			return fmt.Errorf("Unable to parse stats result from '%s': %s", addr, err)
		}
	}
	return nil
}

func (s *intelliflash) importData(r io.Reader, acc telegraf.Accumulator, host string, t analyticsType) error {
	var analytics []analyticsElement
	var measurement string

	resp, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	err = json.Unmarshal(resp, &analytics)
	if err != nil {
		return fmt.Errorf("Error decoding JSON")
	}

	for idx := range analytics {
		for dpname, datapoint := range analytics[idx].Datapoints {
			for midx := range datapoint {
				fields := make(map[string]interface{})

				tags := map[string]string{}

				tags["array"] = host
				name := strings.Split(dpname, "/")
				switch t {
				case SYSTEM:
					measurement = analytics[idx].SystemAnalyticsType
					switch strings.ToUpper(analytics[idx].SystemAnalyticsType) {
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
				case DATA:
					measurement = analytics[idx].EntityType
					tags[analytics[idx].EntityType] = analytics[idx].EntityName
					fields[name[0]] = datapoint[midx]
				}
				acc.AddFields(measurement, fields, tags, time.Unix(analytics[idx].Timestamps[midx]/1000, 0))
			}

		}
	}
	return nil
}

func (s *intelliflash) doRequest(URL string, data <-chan []byte, results chan<- workerResponse) {
	var zebexception zebiException
	if s.client == nil {
		tlsCfg, err := s.ClientConfig.TLSConfig()
		if err != nil {
			results <- workerResponse{nil, err}
			close(results)
			return
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
		results <- workerResponse{nil, err}
		close(results)
		return
	}

	req, err := http.NewRequest("POST", URL, bytes.NewBuffer(<-data))
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
		results <- workerResponse{nil, fmt.Errorf("Username or password not set")}
		close(results)
		return
	}

	res, err := s.client.Do(req)
	if err != nil {
		results <- workerResponse{nil, fmt.Errorf("Unable to connect to intelliflash API '%s': %s", addr, err)}
		close(results)
		return
	}

	if res.StatusCode != 200 {
		errortxt := fmt.Sprintf("Unable to get valid stat result from '%s', http response code : %d", addr, res.StatusCode)
		if s.Debug {
			zebiresp, err := ioutil.ReadAll(res.Body)
			if err == nil {
				json.Unmarshal(zebiresp, &zebexception)
				errortxt = fmt.Sprintf("%s, ZEBI error '%s'", errortxt, zebexception.Message)
			}
		}
		results <- workerResponse{nil, fmt.Errorf(errortxt)}
		close(results)
		return
	}
	results <- workerResponse{res, nil}
	close(results)
}

func emptyThenNull(str string) string {
	if len(str) > 0 {
		return `["` + str + `"]`
	}
	return "null"
}

func init() {
	inputs.Add("intelliflash", func() telegraf.Input {
		return &intelliflash{
			ResponseTimeout: internal.Duration{Duration: defaultResponseTimeout},
			SysMetrics:      nil,
			DataMetrics:     nil,
			Debug:           false,
		}
	})
}
