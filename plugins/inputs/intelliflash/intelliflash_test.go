package intelliflash

import (
	"strings"
	"testing"

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
	},
	{
	  "systemAnalyticsType": "NETWORK",
	  "timestamps": [1565474000000],
	  "datapoints": {
		"Controller-B/IG/mgmt0/Transmit_Mbps": [ 0 ]
	  },
	  "averages": {
		"Controller-B/IG/mgmt0/Transmit_Mbps": 0
	  }
	},
	{
	  "systemAnalyticsType": "CACHE_HITS",
	  "timestamps": [
		1565474000000
	  ],
	  "datapoints": {
		"Controller-A/SSD_Reads": [0]
	  },
	  "averages": {
		"Controller-A/SSD_Reads": 0
	  }
	},
	{
	  "systemAnalyticsType": "POOL_PERFORMANCE",
	  "timestamps": [1565474000000],
	  "datapoints": {
		"pool-a/Data/Write_MBps": [0.12]
	  },
	  "averages": {
		"pool-a/Data/Write_MBps": 0.12
	  }
	}
  ]`

func TestEmptyUsernamePassword(t *testing.T) {
	i := &intelliflash{
		Servers:  []string{"https://localhost/test"},
		Username: "",
		Password: "",
	}
	var acc testutil.Accumulator
	i.Gather(&acc)
	require.Contains(t, acc.Errors[0].Error(), "Username or password not set")
}

func TestConnectionFailure(t *testing.T) {
	i := &intelliflash{
		Servers:  []string{"https://localhost/test"},
		Username: "admin",
		Password: "admin",
	}

	var acc testutil.Accumulator
	i.Gather(&acc)
	require.Contains(t, acc.Errors[0].Error(), "Unable to connect to intelliflash server")
}

func TestCorrectJSON(t *testing.T) {
	i := &intelliflash{
		Servers:  []string{"https://localhost/test"},
		Username: "admin",
		Password: "admin",
	}
	var acc testutil.Accumulator

	err := i.importData(strings.NewReader(testResponse), &acc, "localhost")
	require.NoError(t, err)
}

func TestIncorrectJSON(t *testing.T) {
	i := &intelliflash{
		Servers:  []string{"https://localhost/test"},
		Username: "admin",
		Password: "admin",
	}
	var acc testutil.Accumulator

	err := i.importData(strings.NewReader(incorrectJSON), &acc, "localhost")
	require.Error(t, err)
}
