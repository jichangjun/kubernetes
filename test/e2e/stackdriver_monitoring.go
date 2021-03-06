/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"context"
	"fmt"
	"math"
	"time"

	"golang.org/x/oauth2/google"

	. "github.com/onsi/ginkgo"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/kubernetes/test/e2e/common"
	"k8s.io/kubernetes/test/e2e/framework"

	gcm "google.golang.org/api/monitoring/v3"
)

var (
	// Stackdriver container metrics, as descirbed here:
	// https://cloud.google.com/monitoring/api/metrics#gcp-container
	stackdriverMetrics = []string{
		"uptime",
		"memory/bytes_total",
		"memory/bytes_used",
		"cpu/reserved_cores",
		"cpu/usage_time",
		"memory/page_fault_count",
		"disk/bytes_used",
		"disk/bytes_total",
		"cpu/utilization",
	}

	pollFrequency = time.Second * 5
	pollTimeout   = time.Minute * 7

	rcName            = "resource-consumer"
	replicas          = 1
	cpuUsed           = 100
	cpuLimit    int64 = 200
	memoryUsed        = 64
	memoryLimit int64 = 200
	tolerance         = 0.25
)

var _ = framework.KubeDescribe("Stackdriver Monitoring", func() {
	BeforeEach(func() {
		framework.SkipUnlessProviderIs("gke")
	})

	f := framework.NewDefaultFramework("stackdriver-monitoring")

	It("should have cluster metrics [Feature:StackdriverMonitoring]", func() {
		projectId := framework.TestContext.CloudConfig.ProjectID

		ctx := context.Background()
		client, err := google.DefaultClient(ctx, gcm.CloudPlatformScope)
		gcmService, err := gcm.New(client)
		framework.ExpectNoError(err)

		rc := common.NewDynamicResourceConsumer(rcName, common.KindDeployment, replicas, cpuUsed, memoryUsed, 0, cpuLimit, memoryLimit, f)
		defer rc.CleanUp()

		rc.WaitForReplicas(replicas)

		pollingFunction := checkForMetrics(projectId, gcmService, time.Now())
		framework.ExpectNoError(wait.Poll(pollFrequency, pollTimeout, pollingFunction))
	})
})

func checkForMetrics(projectId string, gcmService *gcm.Service, start time.Time) func() (bool, error) {
	return func() (bool, error) {
		// TODO: list which metrics are missing in case of failure
		counter := 0
		correctUtilization := false
		for _, metric := range stackdriverMetrics {
			// TODO: check only for metrics from this cluster
			ts, err := fetchTimeSeries(projectId, gcmService, metric, start, time.Now())
			framework.ExpectNoError(err)
			if len(ts) > 0 {
				counter = counter + 1
			}

			var sum float64 = 0
			switch metric {
			case "cpu/utilization":
				for _, t := range ts {
					max := t.Points[0]
					maxEnd, _ := time.Parse(time.RFC3339, max.Interval.EndTime)
					for _, p := range t.Points {
						pEnd, _ := time.Parse(time.RFC3339, p.Interval.EndTime)
						if pEnd.After(maxEnd) {
							max = p
							maxEnd, _ = time.Parse(time.RFC3339, max.Interval.EndTime)
						}
					}
					sum = sum + *max.Value.DoubleValue
				}
				if math.Abs(sum*float64(cpuLimit)-float64(cpuUsed)) > tolerance*float64(cpuUsed) {
					return false, nil
				} else {
					correctUtilization = true
				}
			}
		}
		if counter < 9 || !correctUtilization {
			return false, nil
		}
		return true, nil
	}
}

func createMetricFilter(metric string, container_name string) string {
	return fmt.Sprintf(`metric.type="container.googleapis.com/container/%s" AND
				resource.label.container_name="%s"`, metric, container_name)
}

func fetchTimeSeries(projectId string, gcmService *gcm.Service, metric string, start time.Time, end time.Time) ([]*gcm.TimeSeries, error) {
	response, err := gcmService.Projects.TimeSeries.
		List(fullProjectName(projectId)).
		Filter(createMetricFilter(metric, rcName)).
		IntervalStartTime(start.Format(time.RFC3339)).
		IntervalEndTime(end.Format(time.RFC3339)).
		Do()
	if err != nil {
		return nil, err
	}
	return response.TimeSeries, nil
}

func fullProjectName(name string) string {
	return fmt.Sprintf("projects/%s", name)
}
