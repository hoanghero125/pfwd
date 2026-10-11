package detect

import "testing"

func TestLabel(t *testing.T) {
	cases := []struct {
		port    uint16
		process string
		want    string
	}{
		{5173, "node", "Vite"},
		{3000, "node", "dev server"},
		{8000, "python3", "Django/uvicorn"},
		{8888, "", "Jupyter"},
		{9999, "jupyter-lab", "Jupyter"},
		{9999, "jupyter-notebook", "Jupyter"},
		{6006, "", "TensorBoard"},
		{7000, "tensorboard", "TensorBoard"},
		{5432, "", "Postgres"},
		{5433, "postgres", "Postgres"},
		{3306, "", "MySQL"},
		{3307, "mariadbd", "MySQL"},
		{3307, "mysqld", "MySQL"},
		{6379, "", "Redis"},
		{6380, "redis-server", "Redis"},
		{27017, "", "MongoDB"},
		{27018, "mongod", "MongoDB"},
		{9090, "", "Prometheus"},
		{9091, "prometheus", "Prometheus"},
		{4321, "", ""},
		{4321, "myapp", ""},
	}
	for _, c := range cases {
		if got := Label(c.port, c.process); got != c.want {
			t.Errorf("Label(%d, %q) = %q, want %q", c.port, c.process, got, c.want)
		}
	}
}

func TestSkipHTTPProbe(t *testing.T) {
	cases := []struct {
		port    uint16
		process string
		want    bool
	}{
		{5432, "", true},
		{5432, "postgres", true},
		{6543, "postgres", true},
		{3306, "", true},
		{6379, "", true},
		{6380, "redis-server", true},
		{27017, "", true},
		{27017, "mongod", true},
		{5432, "node", false}, // a known non-database process is probed
		{5432, "docker-proxy", true}, // a container port proxy says nothing about the service
		{6379, "rootlessport", true},
		{3306, "pasta", true},
		{8080, "docker-proxy", false},
		{5173, "node", false},
		{8000, "", false},
	}
	for _, c := range cases {
		if got := SkipHTTPProbe(c.port, c.process); got != c.want {
			t.Errorf("SkipHTTPProbe(%d, %q) = %v, want %v", c.port, c.process, got, c.want)
		}
	}
}
