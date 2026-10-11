package detect

import "strings"

// Label names a well-known service from its process name, or else its
// port. It returns "" when neither is known. User labels from the config
// win over these; the engine applies them.
func Label(port uint16, process string) string {
	if l := processLabel(process); l != "" {
		return l
	}
	switch port {
	case 3000:
		return "dev server"
	case 5173:
		return "Vite"
	case 8000:
		return "Django/uvicorn"
	case 8888:
		return "Jupyter"
	case 6006:
		return "TensorBoard"
	case 5432:
		return "Postgres"
	case 3306:
		return "MySQL"
	case 6379:
		return "Redis"
	case 27017:
		return "MongoDB"
	case 9090:
		return "Prometheus"
	}
	return ""
}

func processLabel(process string) string {
	switch {
	case process == "":
		return ""
	case strings.HasPrefix(process, "jupyter"):
		return "Jupyter"
	case strings.HasPrefix(process, "tensorboard"):
		return "TensorBoard"
	case strings.HasPrefix(process, "postgres"):
		return "Postgres"
	case process == "mysqld" || process == "mariadbd":
		return "MySQL"
	case process == "redis-server":
		return "Redis"
	case process == "mongod":
		return "MongoDB"
	case process == "prometheus":
		return "Prometheus"
	}
	return ""
}

// SkipHTTPProbe reports whether a port is a database, which the HTTP probe
// leaves alone: a stray HTTP request shows up in database logs as a
// protocol error, or worse. A port with a known non-database process is
// probed even on a database's usual port.
func SkipHTTPProbe(port uint16, process string) bool {
	switch processLabel(process) {
	case "Postgres", "MySQL", "Redis", "MongoDB":
		return true
	}
	if process != "" && !isPortProxy(process) {
		return false
	}
	switch port {
	case 5432, 3306, 6379, 27017:
		return true
	}
	return false
}

// isPortProxy reports whether process only relays a published container
// port (Docker, rootless Docker and Podman), so its name says nothing about
// the service behind it.
func isPortProxy(process string) bool {
	switch process {
	case "docker-proxy", "rootlessport", "rootlesskit", "slirp4netns", "pasta", "pasta.avx2", "gvproxy":
		return true
	}
	return false
}
