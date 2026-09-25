package validate

import "testing"

func TestStackName(t *testing.T) {
	good := []string{"a", "my-app", "0app", "a-b-c-1"}
	bad := []string{"", "-app", "My-App", "my_app", "a.b", "a/b", string(make([]byte, 70))}
	for _, s := range good {
		if err := StackName(s); err != nil {
			t.Errorf("%q should be valid: %v", s, err)
		}
	}
	for _, s := range bad {
		if StackName(s) == nil {
			t.Errorf("%q should be invalid", s)
		}
	}
	long := "a"
	for len(long) < 63 {
		long += "b"
	}
	if StackName(long) != nil {
		t.Error("63 chars should be valid")
	}
	if StackName(long+"c") == nil {
		t.Error("64 chars should be invalid")
	}
}

func TestComposePath(t *testing.T) {
	cases := map[string]string{
		"docker-compose.yml":     "docker-compose.yml",
		"deploy/compose.yaml":    "deploy/compose.yaml",
		"./deploy//compose.yaml": "deploy/compose.yaml",
	}
	for in, want := range cases {
		got, err := ComposePath(in)
		if err != nil || got != want {
			t.Errorf("ComposePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"", "/etc/passwd", "../x.yml", "a/../../x.yml", "a/../b.yml", "..", ".", "./", "a\\..\\b", "a\nb"}
	for _, s := range bad {
		if _, err := ComposePath(s); err == nil {
			t.Errorf("ComposePath(%q) should fail", s)
		}
	}
}

func TestBranch(t *testing.T) {
	for _, s := range []string{"main", "release/1.2", "feature_x-y", "v1.0.0"} {
		if err := Branch(s); err != nil {
			t.Errorf("%q should be valid: %v", s, err)
		}
	}
	for _, s := range []string{"", "-main", "--upload-pack=x", "a b", "a;b", "a..b", "/a", "a/", "a.lock", "$(x)", "a~1", "a:b"} {
		if Branch(s) == nil {
			t.Errorf("%q should be invalid", s)
		}
	}
}

func TestEnvKey(t *testing.T) {
	for _, s := range []string{"A", "_A", "DB_PASSWORD", "a1"} {
		if err := EnvKey(s); err != nil {
			t.Errorf("%q should be valid", s)
		}
	}
	for _, s := range []string{"", "1A", "A-B", "A B", "A=B", "Ä"} {
		if EnvKey(s) == nil {
			t.Errorf("%q should be invalid", s)
		}
	}
	if EnvValue("K", "a\nb") == nil || EnvValue("K", "a\rb") == nil {
		t.Error("newline values must be rejected")
	}
	if EnvValue("K", "has 'quotes' and $dollar") != nil {
		t.Error("quotes should be allowed")
	}
}

func TestRepoURL(t *testing.T) {
	for _, s := range []string{"https://github.com/o/r", "https://github.com/o/r.git", "https://git.example.com:8443/o/r"} {
		if err := RepoURL(s); err != nil {
			t.Errorf("%q should be valid: %v", s, err)
		}
	}
	for _, s := range []string{"", "http://github.com/o/r", "git@github.com:o/r.git", "ssh://github.com/o/r", "https://user:pw@github.com/o/r", "https://github.com", "file:///etc", "https://github.com/o/r?x=1", "-https://x/y"} {
		if RepoURL(s) == nil {
			t.Errorf("%q should be invalid", s)
		}
	}
}
