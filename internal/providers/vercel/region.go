package vercel

import "regexp"

// vercelRegionCode matches Vercel edge region ids such as arn1 and iad1.
var vercelRegionCode = regexp.MustCompile(`^[a-z]{3}[0-9]$`)

// Region maps Shipyard's configured location onto a Vercel region.
// The EU default (and any other europe-* location) becomes arn1, Stockholm.
// A value that is already a Vercel region code is kept. Known US Cloud Run
// locations map to a US Vercel region. Anything else stays in the EU.
func Region(configured string) string {
	switch configured {
	case "", "eu", "europe-west1", "europe-west4", "europe-north1", "europe-west3", "europe-central2":
		return "arn1"
	case "us-central1", "us-east1", "us-east4":
		return "iad1"
	case "us-west1", "us-west2":
		return "sfo1"
	}
	if vercelRegionCode.MatchString(configured) {
		return configured
	}
	return "arn1"
}

// FrameworkPreset maps a detected framework onto a Vercel project setting.
// Python and generic Node are omitted; Vercel infers those from the files.
func FrameworkPreset(framework string) string {
	switch framework {
	case "nextjs", "vite":
		return framework
	default:
		return ""
	}
}
