package export

// AggregatedSampleProvider presents metrics from several different
// SampleProviders as one.
type AggregatedSampleProvider struct {
	providers []SampleProvider
}

func (p *AggregatedSampleProvider) Samples(sink chan<- *Sample) {
	// since sample providers are relatively unlikely to block
	// it's probably not a huge win to parallelize this.
	for _, provider := range p.providers {
		provider.Samples(sink)
	}
}

// TODO: allow filtering out duplicate series?

// SortedSampleProvider sorts samples so as to conform to the requirements
// of the Prometheus exposition format (all series in a family must be
// together).
type SortedSampleProvider struct {
	inputProvider SampleProvider
}

func (p *SortedSampleProvider) Samples(sink chan<- *Sample) {
	// TODO: also sort within families?
	// TODO: move this logic up in the chain so we can make this a merge-sort style 
	// operation?
	families := make(map[string][]*Sample)

	src := make(chan *Sample, 100)
	go func() {
		defer close(src)
		p.inputProvider.Samples(src)
	}()

	for sample := range src {
		// TODO: optimize for already-sorted case?
		families[sample.Name] = append(families[sample.Name], sample)
	}

	for _, samples := range families {
		for _, sample := range samples {
			sink <- sample
		}
	}
}
