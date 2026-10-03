package backend

const defaultStreamsPerCred = 5

func profileDefaults(p Profile) Profile {
	if p.StreamsPerCred == 0 {
		p.StreamsPerCred = defaultStreamsPerCred
	}
	return p
}
