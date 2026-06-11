package config

type Driver string

const (
	DriverMySQL Driver = "mysql"
	DriverDoris Driver = "doris"
	DriverMongo Driver = "mongo"
	DriverRedis Driver = "redis"
)

type Profile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Driver    Driver `json:"driver"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	User      string `json:"user"`
	Password  string `json:"password"`
	Database  string `json:"database"`
	AuthDB    string `json:"auth_db,omitempty"`
	RedisDB   int    `json:"redis_db,omitempty"`
	URIParams string `json:"uri_params,omitempty"`
	ReadOnly  bool   `json:"read_only"`
}

type Vault struct {
	Profiles []Profile `json:"profiles"`
	// AIProvider is the preferred local AI CLI ("claude" or "codex"); empty means
	// auto-detect from PATH.
	AIProvider string `json:"ai_provider,omitempty"`
}

func (v *Vault) UpsertProfile(profile Profile) {
	for i := range v.Profiles {
		if v.Profiles[i].ID == profile.ID {
			v.Profiles[i] = profile
			return
		}
	}
	v.Profiles = append(v.Profiles, profile)
}

func (v *Vault) DeleteProfile(id string) bool {
	for i := range v.Profiles {
		if v.Profiles[i].ID == id {
			v.Profiles = append(v.Profiles[:i], v.Profiles[i+1:]...)
			return true
		}
	}
	return false
}

func (v Vault) GetProfile(id string) (Profile, bool) {
	for _, profile := range v.Profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return Profile{}, false
}
