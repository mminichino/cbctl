package capellafile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Editor makes comment-preserving edits to a config file.
//
// The file is owned by cbctl: commands change it, users do not have to. Edits go through
// yaml.v3's Node API, which keeps comments and key order. Limits, by design:
//
//   - Files written by cbctl use one canonical encoder (two-space indent, flow style for small
//     leaf maps and lists, see FlowKeys), so a cbctl-written file saved again without edits is
//     byte-identical.
//   - A hand-edited file may be re-indented and normalized on its first save (blank lines between
//     entries are not kept); comments and key order are kept. A .bak of the previous text is
//     always written.
//   - Files that use YAML anchors or aliases are refused (*AnchorError): yaml.v3 expands aliases
//     when it writes, which would change the file's meaning for its author.
type Editor struct {
	// Path is the file being edited.
	Path string
	// Original is the text the file had on disk when it was opened (or last saved). Save refuses
	// to overwrite a file that no longer matches it.
	Original string

	doc *yaml.Node
}

// AnchorError reports a file that uses YAML anchors or aliases.
type AnchorError struct{ File string }

// Error implements error.
func (e *AnchorError) Error() string {
	return fmt.Sprintf("%s uses YAML anchors/aliases, which cbctl cannot edit safely; edit the file by hand or remove them", e.File)
}

// ChangedError reports a file that changed on disk after the editor read it.
type ChangedError struct{ File string }

// Error implements error.
func (e *ChangedError) Error() string {
	return fmt.Sprintf("%s changed while the command was running; nothing was written", e.File)
}

// AmbiguousError reports a cluster name that exists in several projects.
type AmbiguousError struct {
	Name     string
	Projects []string
}

// Error implements error.
func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("cluster %s is in several projects (%s); pass --project NAME", e.Name, strings.Join(e.Projects, ", "))
}

var templateRes = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^[ \t]*# This project has no clusters yet\..*\n?`),
	regexp.MustCompile(`(?m)^[ \t]*# No projects left in Capella\..*\n[ \t]*# or add an existing one with:.*\n?`),
}

func stripTemplates(text string) string {
	for _, re := range templateRes {
		text = re.ReplaceAllString(text, "")
	}
	return text
}

// OpenEditor reads the file at path for editing. The comment blocks "sync" writes into empty
// projects and empty documents are dropped.
func OpenEditor(path string) (*Editor, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	e := &Editor{Path: path, Original: string(b)}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.TrimPrefix(stripTemplates(e.Original), "\ufeff")), &doc); err != nil {
		issue := yamlIssue(err, filepath.Base(path))
		return nil, NewConfigError(filepath.Base(path), []Issue{issue})
	}
	if hasAnchorsOrAliases(&doc) {
		return nil, &AnchorError{File: filepath.Base(path)}
	}
	switch {
	case doc.Kind == 0 || (doc.Kind == yaml.DocumentNode && len(doc.Content) == 0):
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	case doc.Kind != yaml.DocumentNode || doc.Content[0].Kind != yaml.MappingNode:
		return nil, NewConfigError(filepath.Base(path), []Issue{{Path: "(root)", Where: filepath.Base(path),
			Message: filepath.Base(path) + " must be a mapping of settings (key: value)", Line: 1}})
	}
	e.doc = &doc
	return e, nil
}

func (e *Editor) root() *yaml.Node { return e.doc.Content[0] }

// Text returns the canonical encoding of the current document.
func (e *Editor) Text() (string, error) {
	return EncodeNode(e.doc)
}

// Save writes text to the file. The file's current on-disk text is first written to <Path>.bak
// (mode 0600, overwritten), then Path is replaced atomically (temp file in the same directory,
// then rename), keeping the original file mode. It returns the .bak path, or "" when nothing
// was written because text equals the on-disk text. It fails with *ChangedError when the file
// no longer matches Original. A file that does not exist yet is created (mode 0644, no .bak).
func (e *Editor) Save(text string) (backup string, err error) {
	current, readErr := os.ReadFile(e.Path)
	exists := readErr == nil
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		return "", fmt.Errorf("reading %s: %w", e.Path, readErr)
	}
	if exists {
		if string(current) != e.Original {
			return "", &ChangedError{File: filepath.Base(e.Path)}
		}
		if string(current) == text {
			return "", nil
		}
	}
	mode := fs.FileMode(0o644)
	if exists {
		info, err := os.Stat(e.Path)
		if err != nil {
			return "", fmt.Errorf("reading %s: %w", e.Path, err)
		}
		mode = info.Mode().Perm()
		backup = e.Path + ".bak"
		if err := writeFileMode(backup, current, 0o600); err != nil {
			return "", fmt.Errorf("writing %s: %w", backup, err)
		}
	}
	if err := writeAtomic(e.Path, []byte(text), mode); err != nil {
		return "", fmt.Errorf("writing %s: %w", e.Path, err)
	}
	e.Original = text
	return backup, nil
}

func writeFileMode(path string, data []byte, mode fs.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

// writeAtomic replaces path with data via a temp file in the same directory. Where rename cannot
// replace an existing file (Windows), it falls back to copy and remove.
func writeAtomic(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		if copyErr := copyReplace(tmpName, path, mode); copyErr != nil {
			cleanup()
			return err
		}
		cleanup()
	}
	return nil
}

func copyReplace(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }() // read-only handle
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// ---- projects

// projectsNode returns the value node of projects:, or nil when the file has the single-project
// form or neither.
func (e *Editor) projectsNode() *yaml.Node {
	v, _ := mapGet(e.root(), "projects")
	if v != nil && v.Kind == yaml.SequenceNode {
		return v
	}
	return nil
}

// EnsureProjects converts the single-project form (project: + clusters:) to projects: in place
// and creates an empty projects: list when the file has neither. Comments are kept.
func (e *Editor) EnsureProjects() {
	root := e.root()
	if e.projectsNode() != nil {
		return
	}
	if v, i := mapGet(root, "projects"); v != nil { // projects: [] written as null
		root.Content[i+1] = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		return
	}
	projVal, projIdx := mapGet(root, "project")
	if projVal == nil {
		PutKey(root, "projects", &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}, RootKeyOrder)
		return
	}
	project := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if projVal.Kind == yaml.MappingNode {
		project.Content = append(project.Content, projVal.Content...)
		project.HeadComment, project.LineComment, project.FootComment = projVal.HeadComment, projVal.LineComment, projVal.FootComment
	}
	if clustersVal, clustersIdx := mapGet(root, "clusters"); clustersVal != nil {
		keyNode := root.Content[clustersIdx]
		project.Content = append(project.Content, keyNode, clustersVal)
		root.Content = append(root.Content[:clustersIdx], root.Content[clustersIdx+2:]...)
		if clustersIdx < projIdx {
			projIdx -= 2
		}
	}
	root.Content[projIdx].Value = "projects"
	root.Content[projIdx+1] = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{project}}
}

// projectNodes returns the project mapping nodes of either file form.
func (e *Editor) projectNodes() []*yaml.Node {
	if seq := e.projectsNode(); seq != nil {
		var out []*yaml.Node
		for _, p := range seq.Content {
			if p.Kind == yaml.MappingNode {
				out = append(out, p)
			}
		}
		return out
	}
	if v, _ := mapGet(e.root(), "project"); v != nil && v.Kind == yaml.MappingNode {
		return []*yaml.Node{v}
	}
	return nil
}

// ProjectNames returns the project names of either file form.
func (e *Editor) ProjectNames() []string {
	var names []string
	for _, p := range e.projectNodes() {
		v, _ := mapGet(p, "name")
		names = append(names, scalarString(v))
	}
	return names
}

// FindProject returns the project mapping with the given name.
func (e *Editor) FindProject(name string) (*yaml.Node, bool) {
	for _, p := range e.projectNodes() {
		if v, _ := mapGet(p, "name"); scalarString(v) == name {
			return p, true
		}
	}
	return nil, false
}

// AddProject appends a project, converting to the projects: form first.
func (e *Editor) AddProject(obj Obj) {
	e.EnsureProjects()
	seq := e.projectsNode()
	seq.Style = 0
	seq.Content = append(seq.Content, ToNode(obj, "projects"))
}

// RemoveProject removes the project with the given name, converting to the projects: form first.
// It reports whether the project was found.
func (e *Editor) RemoveProject(name string) bool {
	if _, ok := e.FindProject(name); !ok {
		return false
	}
	e.EnsureProjects()
	seq := e.projectsNode()
	for i, p := range seq.Content {
		if v, _ := mapGet(p, "name"); scalarString(v) == name {
			seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
			if len(seq.Content) == 0 {
				seq.Style = yaml.FlowStyle
			}
			return true
		}
	}
	return false
}

// ---- clusters

// clustersNode returns the clusters sequence that belongs to project, creating it when create is
// set. In the single-project form the sequence lives at the top level.
func (e *Editor) clustersNode(project *yaml.Node, create bool) *yaml.Node {
	holder, order := project, ProjectKeyOrder
	if v, _ := mapGet(e.root(), "project"); v == project {
		holder, order = e.root(), RootKeyOrder
	}
	seq, _ := mapGet(holder, "clusters")
	if seq == nil || seq.Kind != yaml.SequenceNode {
		if !create {
			return nil
		}
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		PutKey(holder, "clusters", seq, order)
		seq, _ = mapGet(holder, "clusters")
	}
	return seq
}

// FindCluster finds a cluster by name. When project is "" and the name exists in more than one
// project the result is an *AmbiguousError. A cluster that does not exist is (nil, nil, nil).
func (e *Editor) FindCluster(name, project string) (proj, cluster *yaml.Node, err error) {
	type hit struct{ proj, cluster *yaml.Node }
	var hits []hit
	var projects []string
	for _, p := range e.projectNodes() {
		pname := ""
		if v, _ := mapGet(p, "name"); v != nil {
			pname = v.Value
		}
		if project != "" && pname != project {
			continue
		}
		seq := e.clustersNode(p, false)
		if seq == nil {
			continue
		}
		for _, c := range seq.Content {
			if v, _ := mapGet(c, "name"); c.Kind == yaml.MappingNode && scalarString(v) == name {
				hits = append(hits, hit{p, c})
				if !containsKey(projects, pname) {
					projects = append(projects, pname)
				}
			}
		}
	}
	if len(projects) > 1 {
		sort.Strings(projects)
		return nil, nil, &AmbiguousError{Name: name, Projects: projects}
	}
	if len(hits) == 0 {
		return nil, nil, nil
	}
	return hits[0].proj, hits[0].cluster, nil
}

// ClusterNodes returns the cluster mappings of project, in either file form.
func (e *Editor) ClusterNodes(project *yaml.Node) []*yaml.Node {
	seq := e.clustersNode(project, false)
	if seq == nil {
		return nil
	}
	var out []*yaml.Node
	for _, c := range seq.Content {
		if c.Kind == yaml.MappingNode {
			out = append(out, c)
		}
	}
	return out
}

// AddCluster appends a cluster to project.
func (e *Editor) AddCluster(project *yaml.Node, cluster Obj) {
	seq := e.clustersNode(project, true)
	seq.Style = 0
	seq.Content = append(seq.Content, ToNode(cluster, "clusters"))
}

// RemoveCluster removes the cluster with the given name from project.
func (e *Editor) RemoveCluster(project *yaml.Node, name string) {
	seq := e.clustersNode(project, false)
	if seq == nil {
		return
	}
	for i, c := range seq.Content {
		if v, _ := mapGet(c, "name"); scalarString(v) == name {
			seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
			return
		}
	}
}

// Group returns the service group named by services (for example "data,index,query", in any
// order), or, when services is "", the one group that runs the data service.
func (e *Editor) Group(cluster *yaml.Node, services string) (*yaml.Node, error) {
	groupsNode, _ := mapGet(cluster, "serviceGroups")
	var groups []*yaml.Node
	if groupsNode != nil {
		groups = groupsNode.Content
	}
	keyOf := func(g *yaml.Node) string {
		v, _ := mapGet(g, "services")
		var list []string
		if v != nil {
			for _, s := range v.Content {
				list = append(list, s.Value)
			}
		}
		sort.Strings(list)
		return strings.Join(list, ",")
	}
	if services != "" {
		var parts []string
		for _, s := range strings.Split(services, ",") {
			if s = strings.TrimSpace(s); s != "" {
				parts = append(parts, s)
			}
		}
		sort.Strings(parts)
		key := strings.Join(parts, ",")
		for _, g := range groups {
			if keyOf(g) == key {
				return g, nil
			}
		}
		have := make([]string, 0, len(groups))
		for _, g := range groups {
			have = append(have, keyOf(g))
		}
		return nil, fmt.Errorf("no service group '%s' (groups: %s)", key, strings.Join(have, ", "))
	}
	var withData []*yaml.Node
	for _, g := range groups {
		if containsKey(strings.Split(keyOf(g), ","), "data") {
			withData = append(withData, g)
		}
	}
	switch len(withData) {
	case 1:
		return withData[0], nil
	case 0:
		return nil, errors.New("no service group runs data")
	}
	return nil, errors.New("more than one group runs data")
}

// ---- edits

// Set sets the value at path below node, creating intermediate mappings. A new key is inserted
// where the canonical key order puts it; an existing value keeps its comments.
func (e *Editor) Set(node *yaml.Node, path []string, value any) {
	e.SetOrdered(node, path, value, inferOrder(node))
}

// SetOrdered is Set with an explicit key order for the keys it creates.
func (e *Editor) SetOrdered(node *yaml.Node, path []string, value any, order []string) {
	if len(path) == 0 {
		return
	}
	for _, k := range path[:len(path)-1] {
		next, _ := mapGet(node, k)
		if next == nil || next.Kind != yaml.MappingNode {
			PutKey(node, k, Obj{}, order)
			next, _ = mapGet(node, k)
		}
		node, order = next, nil
	}
	PutKey(node, path[len(path)-1], value, order)
}

// inferOrder picks the key order that fits the mapping: bucket keys for a bucket, cluster keys
// otherwise.
func inferOrder(node *yaml.Node) []string {
	for _, k := range []string{"cloudProvider", "serviceGroups", "buckets", "tier"} {
		if v, _ := mapGet(node, k); v != nil {
			return ClusterKeyOrder
		}
	}
	for _, k := range []string{"scopes", "memoryAllocationInMb", "durabilityLevel", "replicas", "storageBackend"} {
		if v, _ := mapGet(node, k); v != nil {
			return BucketKeyOrder
		}
	}
	return ClusterKeyOrder
}

// Delete removes key from a mapping node.
func (e *Editor) Delete(node *yaml.Node, key string) {
	if _, i := mapGet(node, key); i >= 0 {
		node.Content = append(node.Content[:i], node.Content[i+2:]...)
	}
}

// PutKey sets key in a mapping node. An existing key keeps its position and comments. A new key
// goes before the first existing key that order places after it, or at the end when order does
// not list it.
func PutKey(node *yaml.Node, key string, value any, order []string) {
	val := ToNode(value, key)
	if old, _ := mapGet(node, key); old != nil {
		replaceNode(old, val)
		return
	}
	pos := len(node.Content)
	if idx := indexOf(order, key); idx >= 0 {
		later := map[string]bool{}
		for _, k := range order[idx+1:] {
			later[k] = true
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			if later[node.Content[i].Value] {
				pos = i
				break
			}
		}
	}
	content := make([]*yaml.Node, 0, len(node.Content)+2)
	content = append(content, node.Content[:pos]...)
	content = append(content, scalarNode(key), val)
	content = append(content, node.Content[pos:]...)
	node.Content = content
}

func indexOf(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

// replaceNode overwrites old with val in place, keeping old's comments unless val has its own.
func replaceNode(old, val *yaml.Node) {
	head, line, foot := old.HeadComment, old.LineComment, old.FootComment
	*old = *val
	if old.HeadComment == "" {
		old.HeadComment = head
	}
	if old.LineComment == "" {
		old.LineComment = line
	}
	if old.FootComment == "" {
		old.FootComment = foot
	}
}

// ---- ids

// SetID pins a project or cluster by writing id as its first key. kind is "project" or
// "cluster"; for a cluster, project names the project ("" searches every project). It reports
// whether the resource was found.
func (e *Editor) SetID(kind, project, name, id string) bool {
	node := e.resourceNode(kind, project, name)
	if node == nil {
		return false
	}
	order := ClusterKeyOrder
	if kind == "project" {
		order = ProjectKeyOrder
	}
	PutKey(node, "id", id, order)
	return true
}

// ClearID removes the id key of a project or cluster; see SetID.
func (e *Editor) ClearID(kind, project, name string) bool {
	node := e.resourceNode(kind, project, name)
	if node == nil {
		return false
	}
	e.Delete(node, "id")
	return true
}

func (e *Editor) resourceNode(kind, project, name string) *yaml.Node {
	if kind == "project" {
		p, _ := e.FindProject(name)
		return p
	}
	_, cluster, err := e.FindCluster(name, project)
	if err != nil {
		return nil
	}
	return cluster
}

// ---- generic list edits

// Child returns the value of key in a mapping node, or nil.
func (e *Editor) Child(node *yaml.Node, key string) *yaml.Node {
	v, _ := mapGet(node, key)
	return v
}

// FindBy returns the item of the list node[listKey] whose scalar field equals value, or nil.
func (e *Editor) FindBy(node *yaml.Node, listKey, field, value string) *yaml.Node {
	seq := e.Child(node, listKey)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range seq.Content {
		if v, _ := mapGet(item, field); item.Kind == yaml.MappingNode && scalarString(v) == value {
			return item
		}
	}
	return nil
}

// Append adds obj to the list node[listKey], creating the list where the key order puts it.
func (e *Editor) Append(node *yaml.Node, listKey string, obj any) {
	seq := e.Child(node, listKey)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		PutKey(node, listKey, []any{}, inferOrder(node))
		seq = e.Child(node, listKey)
	}
	seq.Style = 0
	seq.Content = append(seq.Content, ToNode(obj, listKey))
}

// RemoveBy removes the item of the list node[listKey] whose scalar field equals value and
// reports whether there was one. A list that becomes empty is removed.
func (e *Editor) RemoveBy(node *yaml.Node, listKey, field, value string) bool {
	seq := e.Child(node, listKey)
	if seq == nil || seq.Kind != yaml.SequenceNode {
		return false
	}
	for i, item := range seq.Content {
		if v, _ := mapGet(item, field); item.Kind == yaml.MappingNode && scalarString(v) == value {
			seq.Content = append(seq.Content[:i], seq.Content[i+1:]...)
			if len(seq.Content) == 0 {
				e.Delete(node, listKey)
			}
			return true
		}
	}
	return false
}

// Decode returns node as plain JSON-compatible data (integral numbers as int64).
func (e *Editor) Decode(node *yaml.Node) (Obj, error) {
	var raw map[string]any
	if err := node.Decode(&raw); err != nil {
		return nil, err
	}
	out, _ := NormalizeNumbers(raw).(map[string]any)
	return out, nil
}
