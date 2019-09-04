package intelliflash

import (
	"strings"
	"testing"
	"time"

	"github.com/influxdata/telegraf/internal"
	"github.com/influxdata/telegraf/testutil"
	"github.com/stretchr/testify/require"
)

const incorrectJSON = "This is not JSON at all"

const testResponse = `[
	{
	  "systemAnalyticsType": "CPU",
	  "timestamps": [1565473945000],
	  "datapoints": {
		"Controller-A/Total_Used": [0]
	  },
	  "averages": {
		"Controller-A/Total_Used": 0.25
	  }
	}
  ]`

func TestSampleAndDescription(t *testing.T) {
	i := &intelliflash{}
	description := i.Description()
	sampleConfig := i.SampleConfig()
	require.NotEmpty(t, description)
	require.NotEmpty(t, sampleConfig)
}

func TestEmptyServers(t *testing.T) {
	i := &intelliflash{}
	var acc testutil.Accumulator
	err := i.Gather(&acc)
	require.Contains(t, err.Error(), "no servers specified")
}

func TestEmptyUsernamePassword(t *testing.T) {
	i := &intelliflash{
		Servers:         []string{"https://localhost/test"},
		Username:        "",
		Password:        "",
		ResponseTimeout: internal.Duration{Duration: time.Duration(10)},
	}
	var acc testutil.Accumulator
	i.Gather(&acc)
	require.Contains(t, acc.Errors[0].Error(), "Username or password not set")
}

func TestConnectionFailure(t *testing.T) {
	i := &intelliflash{
		Servers:  []string{"https://localhost"},
		Username: "admin",
		Password: "admin",
	}

	var acc testutil.Accumulator
	i.Gather(&acc)
	require.Contains(t, acc.Errors[0].Error(), "Unable to connect to intelliflash")
}

func TestCorrectJSON(t *testing.T) {
	i := &intelliflash{
		Servers:  []string{"https://localhost"},
		Username: "admin",
		Password: "admin",
	}
	var acc testutil.Accumulator

	err := i.importData(strings.NewReader(testResponse), &acc, "localhost", SYSTEM)
	require.NoError(t, err)
}

func TestIncorrectJSON(t *testing.T) {
	i := &intelliflash{
		Servers:  []string{"https://localhost"},
		Username: "admin",
		Password: "admin",
	}
	var acc testutil.Accumulator

	err := i.importData(strings.NewReader(incorrectJSON), &acc, "localhost", SYSTEM)
	require.Error(t, err)
}

func TestMetrics(t *testing.T) {
	i := &intelliflash{
		Servers:    []string{"https://localhost"},
		Username:   "admin",
		Password:   "admin",
		SysMetrics: []string{"CPU", "NETWORK"},
		DataMetrics: []dataMetrics{{
			Protocols: []string{"nfs", "iscsi"},
		}},
	}
	var acc testutil.Accumulator
	i.Gather(&acc)
}
