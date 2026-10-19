package backend

const defaultStreamsPerCred = 5

func profileDefaults(p Profile) Profile {
	if p.UplinkMode == "" {
		p.UplinkMode = "packet"
	}
	if p.Transport == "" {
		p.Transport = "tcp"
	}
	if p.ObfProfile == "" {
		p.ObfProfile = "rtpopus2"
	}
	if p.StreamsPerCred == 0 {
		p.StreamsPerCred = defaultStreamsPerCred
	}
	return p
}
