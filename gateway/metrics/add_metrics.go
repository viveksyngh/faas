package metrics

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"

	"github.com/openfaas/faas/gateway/requests"
)

func makeClient() http.Client {
	// Fine-tune the client to fail fast.
	return http.Client{}
}

// AddMetricsHandler wraps a http.HandlerFunc with Prometheus metrics
func AddMetricsHandler(handler http.HandlerFunc, prometheusQuery PrometheusQueryFetcher) http.HandlerFunc {

	return func(w http.ResponseWriter, r *http.Request) {
		// log.Printf("Calling upstream for function info\n")

		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		upstreamCall := recorder.Result()

		if upstreamCall.Body == nil {
			log.Println("Upstream call had empty body.")
			return
		}

		defer upstreamCall.Body.Close()

		if recorder.Code != http.StatusOK {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf("Error pulling metrics from provider/backend. Status code: %d", recorder.Code)))
			return
		}

		upstreamBody, _ := ioutil.ReadAll(upstreamCall.Body)
		var functions []requests.Function

		err := json.Unmarshal(upstreamBody, &functions)

		if err != nil {
			log.Printf("Metrics upstream error: %s", err)

			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("Error parsing metrics from upstream provider/backend."))
			return
		}

		// log.Printf("Querying Prometheus API\n")
		expr := url.QueryEscape(`sum(gateway_function_invocation_total{function_name=~".*", code=~".*"}) by (function_name, code)`)
		// expr := "sum(gateway_function_invocation_total%7Bfunction_name%3D~%22.*%22%2C+code%3D~%22.*%22%7D)+by+(function_name%2C+code)"
		results, fetchErr := prometheusQuery.Fetch(expr)
		if fetchErr != nil {
			log.Printf("Error querying Prometheus API: %s\n", fetchErr.Error())
			writeResponse(w, upstreamBody)
			return
		}

		invocationCount2XXExpr := url.QueryEscape(`sum(gateway_function_invocation_total {function_name=~".*", code=~"2.*"}) by (function_name)`)
		invocationCount2XXResults, fetchErr := prometheusQuery.Fetch(invocationCount2XXExpr)

		if fetchErr != nil {
			log.Printf("Error querying Prometheus API: %s\n", fetchErr.Error())
			writeResponse(w, upstreamBody)
			return
		}

		invocationCountNon2XXExpr := url.QueryEscape(`sum(gateway_function_invocation_total {function_name=~".*", code!~"2.*"}) by (function_name)`)
		invocationCountNon2XXResults, fetchErr := prometheusQuery.Fetch(invocationCountNon2XXExpr)

		if fetchErr != nil {
			log.Printf("Error querying Prometheus API: %s\n", fetchErr.Error())
			writeResponse(w, upstreamBody)
			return
		}

		averageResponseTimeExpr := url.QueryEscape(`avg(gateway_functions_seconds_sum/gateway_functions_seconds_count {function_name=~".*"}) by (function_name)`)
		averageResponseTimeResults, fetchErr := prometheusQuery.Fetch(averageResponseTimeExpr)

		if fetchErr != nil {
			log.Printf("Error querying Prometheus API: %s\n", fetchErr.Error())
			writeResponse(w, upstreamBody)
			return
		}

		mixIn(&functions, results, invocationCount2XXResults, invocationCountNon2XXResults, averageResponseTimeResults)

		bytesOut, marshalErr := json.Marshal(functions)
		if marshalErr != nil {
			log.Println(marshalErr)
			return
		}

		// log.Printf("Writing bytesOut: %s\n", bytesOut)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(bytesOut)
	}
}

func mixIn(functions *[]requests.Function, invocationCountMetrics, invocationCount2XXMetrics, invocationCountNon2XXMetrics, averageResponseTimeMetrics *VectorQueryResponse) {
	if functions == nil {
		return
	}

	// Ensure values are empty first.
	for i := range *functions {
		(*functions)[i].InvocationCount = 0
		(*functions)[i].InvocationCount2XX = 0
		(*functions)[i].InvocationCountNon2XX = 0
		(*functions)[i].AverageResponseTime = 0

	}

	for i, function := range *functions {

		for _, v := range invocationCountMetrics.Data.Result {

			if v.Metric.FunctionName == function.Name {
				parsedValue, err := parseMetricValue(v.Value[1])
				if err == nil {
					(*functions)[i].InvocationCount += parsedValue
				}
			}
		}

		for _, v := range invocationCount2XXMetrics.Data.Result {

			if v.Metric.FunctionName == function.Name {
				parsedValue, err := parseMetricValue(v.Value[1])
				if err == nil {
					(*functions)[i].InvocationCount2XX += parsedValue
				}
			}
		}

		for _, v := range invocationCountNon2XXMetrics.Data.Result {

			if v.Metric.FunctionName == function.Name {
				if v.Metric.FunctionName == function.Name {
					parsedValue, err := parseMetricValue(v.Value[1])
					if err == nil {
						(*functions)[i].InvocationCountNon2XX += parsedValue
					}
				}
			}
		}

		for _, v := range averageResponseTimeMetrics.Data.Result {

			if v.Metric.FunctionName == function.Name {
				if v.Metric.FunctionName == function.Name {
					parsedValue, err := parseMetricValue(v.Value[1])
					if err == nil {
						(*functions)[i].AverageResponseTime += parsedValue
					}
				}
			}
		}
	}
}

func writeResponse(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(body)
}

func parseMetricValue(metricValue interface{}) (parsedValue float64, err error) {
	switch metricValue.(type) {
	case string:
		parsedValue, err = strconv.ParseFloat(metricValue.(string), 64)
		if err != nil {
			log.Printf("Unable to convert value for metric: %s\n", err)
		}
		break
	}
	return parsedValue, err
}
