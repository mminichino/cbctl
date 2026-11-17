package yamlio

// TimestampMode says what a YAML timestamp scalar (2026-01-01T00:00:00Z) becomes in JSON.
type TimestampMode int

const (
	// KeepText keeps the text as typed. capella.yaml uses it: expiresAt is written unquoted and
	// stays the string it was.
	KeepText TimestampMode = iota
	// RFC3339 re-formats the value with time.RFC3339Nano (manifest import data).
	RFC3339
)

const (
	// MaxInputBytes is the largest input the package reads.
	MaxInputBytes = 10 << 20
	// DefaultMaxDocuments is the default cap on documents in one stream.
	DefaultMaxDocuments = 64
	// maxNodes caps the nodes written while expanding aliases (billion-laughs protection).
	maxNodes = 1_000_000
	maxDepth = 10_000
)

type options struct {
	source    string
	ts        TimestampMode
	keepNode  bool
	maxDocs   int
	indent    int
	useNumber bool
}

func newOptions(opts []Option) *options {
	o := &options{maxDocs: DefaultMaxDocuments, indent: 2}
	for _, f := range opts {
		f(o)
	}
	return o
}

// Option configures decoding or encoding.
type Option func(*options)

// WithTimestamps selects how timestamp scalars convert (default KeepText).
func WithTimestamps(mode TimestampMode) Option { return func(o *options) { o.ts = mode } }

// WithKeepNode keeps the parsed *yaml.Node in Document.Node, for editors.
func WithKeepNode() Option { return func(o *options) { o.keepNode = true } }

// WithMaxDocuments caps the documents a Decoder reads (default 64).
func WithMaxDocuments(n int) Option { return func(o *options) { o.maxDocs = n } }

// WithSource sets the name shown in error messages, usually the file name.
func WithSource(name string) Option { return func(o *options) { o.source = name } }

// WithIndent sets the YAML indentation for encoding (default 2).
func WithIndent(n int) Option { return func(o *options) { o.indent = n } }

// WithUseNumber decodes JSON numbers that land in an interface{} as json.Number instead of
// float64, so large integers are not rounded.
func WithUseNumber() Option { return func(o *options) { o.useNumber = true } }
