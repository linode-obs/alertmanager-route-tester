package telemetry_test

import "testing"

func TestHTTPTraceConnectsRequestToAlertmanager(t *testing.T) {
	requireInstrumentedApp(t)

	const (
		labelValue   = "private-label-value"
		receiverName = "private-receiver-name"
		requestID    = "private-request-id"
	)

	capture := &telemetryCapture{}
	collector := newOTLPCollector(t, capture)
	alertmanagerServer := newAlertmanagerServer(t, capture, receiverName, labelValue)
	alertmanagerURL, authValue := authenticatedAlertmanagerURL(t, alertmanagerServer.URL)
	appAddress := availableAddress(t)
	configPath := writeAppConfig(t, appAddress, alertmanagerURL.String())
	app := startInstrumentedApp(t, appAddress, configPath, collector.URL)

	app.testRoute(t, labelValue, requestID, receiverName)
	app.reloadConfig(t)
	app.stop(t)

	tracePayloads := capture.assertTraceLink(t, app.output.String())
	capture.assertNoSensitiveTelemetry(t, tracePayloads, []string{
		labelValue,
		receiverName,
		requestID,
		authValue,
		alertmanagerURL.User.String(),
	})
	capture.assertRequiredMetrics(t)
}
