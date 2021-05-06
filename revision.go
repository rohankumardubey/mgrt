// package mgrt provides a collection of functions for performing revisions
// against any given database connection.
package mgrt

import (
	"bytes"
	"bufio"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// node is a node in the binary tree of a Collection. This stores the val used
// for sorting revisions in a Collection. The val will be the Unix time of the
// Revision ID, since Revision IDs are a time in the layout of 20060102150405.
type node struct {
	val   int64
	rev   *Revision
	left  *node
	right *node
}

// Errors is a collection of errors that occurred.
type Errors []error

// Revision is the type that represents what SQL code has been executed against
// a database as a revision. Typically, this would be changes made to the
// database schema itself.
type Revision struct {
	ID         string     // ID is the time when the Revision was added.
	Author     string     // Author is who authored the original Revision.
	Comment    string     // Comment provides a short description for the Revision.
	SQL        string     // SQL is the code that will be executed when the Revision is performed.
	PerformedAt time.Time // PerformedAt is when the Revision was executed.
}

// RevisionError represents an error that occurred with a revision.
type RevisionError struct {
	ID  string // ID is the ID of the revisions that errored.
	Err error  // Err is the underlying error itself.
}

// Collection stores revisions in a binary tree. This ensures that when they are
// retrieved, they will be retrieved in ascending order from when they were
// initially added.
type Collection struct {
	len  int
	root *node
}

var (
	revisionIdFormat = "20060102150405"

	// ErrInvalid is returned whenever an invalid Revision ID is encountered. A
	// Revision ID is considered invalid when the time layout 20060102150405
	// cannot be used for parse the ID.
	ErrInvalid = errors.New("revision id invalid")

	// ErrPerformed is returned whenever a Revision has already been performed.
	// This can be treated as a benign error.
	ErrPerformed = errors.New("revision performed")
)

func insertNode(n **node, val int64, r *Revision) {
	if (*n) == nil {
		(*n) = &node{
			val: val,
			rev: r,
		}
		return
	}

	if val < (*n).val {
		insertNode(&(*n).left, val, r)
		return
	}
	insertNode(&(*n).right, val, r)
}

// NewRevision creates a new Revision with the given author, and comment.
func NewRevision(author, comment string) *Revision {
	return &Revision{
		ID:      time.Now().Format(revisionIdFormat),
		Author:  author,
		Comment: comment,
	}
}

// RevisionPerformed checks to see if the given Revision has been performed
// against the given database.
func RevisionPerformed(db *sql.DB, rev *Revision) error {
	var count int64

	if _, err := time.Parse(revisionIdFormat, rev.ID); err != nil {
		return ErrInvalid
	}

	q := "SELECT COUNT(id) FROM mgrt_revisions WHERE id = " + rev.ID

	if err := db.QueryRow(q).Scan(&count); err != nil {
		return &RevisionError{
			ID:  rev.ID,
			Err: err,
		}
	}

	if count > 0 {
		return &RevisionError{
			ID:  rev.ID,
			Err: ErrPerformed,
		}
	}
	return nil
}

// GetRevisions returns a list of all the Revisions that have been performed
// against the given database. The returned revisions will be ordered by their
// performance date descending.
func GetRevisions(db *sql.DB) ([]*Revision, error) {
	var count int64

	q0 := "SELECT COUNT(id) FROM mgrt_revisions"

	if err := db.QueryRow(q0).Scan(&count); err != nil {
		return nil, err
	}

	revs := make([]*Revision, 0, int(count))

	q := "SELECT id, author, comment, sql, performed_at FROM mgrt_revisions ORDER BY performed_at DESC"

	rows, err := db.Query(q)

	if err != nil {
		return nil, err
	}

	defer rows.Close()

	for rows.Next() {
		var (
			rev Revision
			sec int64
		)

		err = rows.Scan(&rev.ID, &rev.Author, &rev.Comment, &rev.SQL, &sec)

		if err != nil {
			return nil, err
		}

		rev.PerformedAt = time.Unix(sec, 0)
		revs = append(revs, &rev)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	return revs, nil
}

// PerformRevisions will perform the given revisions against the given database.
// The given revisions will be sorted into ascending order first before they
// are performed. If any of the given revisions have already been performed then
// the Errors type will be returned containing *RevisionError for each revision
// that was already performed.
func PerformRevisions(db *sql.DB, revs0 ...*Revision) error {
	var c Collection

	for _, rev := range revs0 {
		c.Put(rev)
	}

	errs := Errors(make([]error, 0, len(revs0)))
	revs := c.Slice()

	for _, rev := range revs {
		if err := rev.Perform(db); err != nil {
			if errors.Is(err, ErrPerformed) {
				errs = append(errs, err)
				continue
			}
			return err
		}
	}
	return errs.err()
}

// OpenRevision opens the revision at the given path.
func OpenRevision(path string) (*Revision, error) {
	f, err := os.Open(path)

	if err != nil {
		return nil, err
	}

	defer f.Close()

	return UnmarshalRevision(f)
}

// UnmarshalRevision will unmarshal a Revision from the given io.Reader. This
// will expect to see a comment block header that contains the metadata about
// the Revision itself. This will check to see if the given Revision ID is
// valid. A Revision id is considered valid when it can be parsed into a
// valid time via time.Parse using the layout of 20060102150405.
func UnmarshalRevision(r io.Reader) (*Revision, error) {
	br := bufio.NewReader(r)

	rev := &Revision{}

	var (
		buf     []rune = make([]rune, 0)
		r0      rune
		inBlock bool
	)

	for {
		r, _, err := br.ReadRune()

		if err != nil {
			if err != io.EOF {
				return nil, err
			}
			rev.SQL = strings.TrimSpace(string(buf))
			break
		}

		if r == '*' {
			if r0 == '/' {
				inBlock = true
				continue
			}
		}

		if r == '/' {
			if r0 == '*' {
				rev.Comment = strings.TrimSpace(string(buf))
				buf = buf[0:0]
				inBlock = false
				continue
			}
		}

		if inBlock {
			if r == '\n' {
				if r0 == '\n' {
					buf = buf[0:0]
					continue
				}

				pos := -1

				for i, r := range buf {
					if r == ':' {
						pos = i
						break
					}
				}

				if pos < 0 {
					goto cont
				}

				if string(buf[pos-6:pos]) == "Author" {
					rev.Author = strings.TrimSpace(string(buf[pos+1:]))
					buf = buf[0:0]
					continue
				}

				if string(buf[pos-8:pos]) == "Revision" {
					rev.ID = strings.TrimSpace(string(buf[pos+1:]))
					buf = buf[0:0]
					continue
				}
			}
		}

		if r == '*' {
			peek, _, err := br.ReadRune()

			if err != nil {
				if err != io.EOF {
					return nil, err
				}
				continue
			}

			if peek == '/' {
				br.UnreadRune()
				r0 = r
				continue
			}
		}

cont:
		buf = append(buf, r)
		r0 = r
	}

	if _, err := time.Parse(revisionIdFormat, rev.ID); err != nil {
		return nil, ErrInvalid
	}
	return rev, nil
}

func (n *node) walk(visit func(*Revision)) {
	if n.left != nil {
		n.left.walk(visit)
	}

	visit(n.rev)

	if n.right != nil {
		n.right.walk(visit)
	}
}

func (e Errors) err() error {
	if len(e) == 0 {
		return nil
	}
	return e
}

// Error returns the string representation of all the errors in the underlying
// slice. Each error will be on a separate line in the returned string.
func (e Errors) Error() string {
	var buf bytes.Buffer

	for _, err := range e {
		buf.WriteString(err.Error() + "\n")
	}
	return buf.String()
}

// Put puts the given Revision in the current Collection.
func (c *Collection) Put(r *Revision) error {
	if r.ID == "" {
		return ErrInvalid
	}

	t, err := time.Parse(revisionIdFormat, r.ID)

	if err != nil {
		return ErrInvalid
	}

	insertNode(&c.root, t.Unix(), r)
	c.len++
	return nil
}

// Len returns the number of items in the collection.
func (c *Collection) Len() int { return c.len }

// Slice returns a sorted slice of all the revisions in the collection.
func (c *Collection) Slice() []*Revision {
	revs := make([]*Revision, 0, c.len)

	c.root.walk(func(r *Revision) {
		revs = append(revs, r)
	})
	return revs
}

func (e *RevisionError) Error() string {
	return "revision error " + e.ID + ": " + e.Err.Error()
}

// Unwrap returns the underlying error that caused the original RevisionError.
func (e *RevisionError) Unwrap() error { return e.Err }

// Perform will perform the current Revision against the given database. If
// the Revision is emtpy, then nothing happens. If the Revision has already
// been performed, then ErrPerformed is returned.
func (r *Revision) Perform(db *sql.DB) error {
	if r.SQL == "" {
		return nil
	}

	if err := RevisionPerformed(db, r); err != nil {
		return err
	}

	if _, err := db.Exec(r.SQL); err != nil {
		return err
	}

	q := fmt.Sprintf(
		"INSERT INTO mgrt_revisions (id, author, comment, sql, performed_at) VALUES (%q, %q, %q, %s, %d)",
		r.ID, r.Author, r.Comment, "'" + r.SQL + "'", time.Now().Unix(),
	)

	if _, err := db.Exec(q); err != nil {
		return err
	}
	return nil
}

// Title will extract the title from the comment of the current Revision. First,
// this will truncate the title to being 72 characters. If the comment was longer
// than 72 characters, then the title will be suffixed with "...". If a LF
// character can be found in the title, then the title will be truncated again
// up to where that LF character occurs.
func (r *Revision) Title() string {
	title := r.Comment

	if l := len(title); l >= 72 {
		title = title[:72]

		if l > 72 {
			title += "..."
		}

		if i := bytes.IndexByte([]byte(title), '\n'); i > 0 {
			title = title[:i]
		}
	}
	return title
}

// String returns the string representation of the Revision. This will be the
// comment block header followed by the Revision SQL itself.
func (r *Revision) String() string {
	var buf bytes.Buffer

	buf.WriteString("/*\n")
	buf.WriteString("Revision: " + r.ID + "\n")
	buf.WriteString("Author:   " + r.Author + "\n")

	if r.Comment != "" {
		buf.WriteString("\n" + r.Comment + "\n")
	}
	buf.WriteString("*/\n\n")
	buf.WriteString(r.SQL)
	return buf.String()
}
