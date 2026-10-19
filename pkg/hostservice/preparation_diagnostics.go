package hostservice

type preparationDiagnostics struct{ server *Server }

func (diagnostics preparationDiagnostics) Write(data []byte) (int, error) {
	diagnostics.server.diagnosticsMu.Lock()
	defer diagnostics.server.diagnosticsMu.Unlock()
	return diagnostics.server.config.Diagnostics.Write(data)
}
