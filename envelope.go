package envelope

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log"
	"net/mail"
	"strconv"
	"strings"
	"time"

	enmime "github.com/jhillyerd/go.enmime"
	"github.com/nycmonkey/tika"
)

// Metadata holds key details about a message from the body of a journal envelope
type Metadata struct {
	Sender, OnBehalfOf, Subject, MessageID string   `json:",omitempty"`
	To, CC, BCC                            []string `json:",omitempty"`
	JournalTimestamp                       time.Time
}

// Message holds the details of a journaled message
type Message struct {
	*Metadata
	BodyText string
}

// Parse reads an email in a journaled envelope, and returns the parsed data
func Parse(in io.Reader) (m *Message, err error) {
	// envelope refers to a journaled email message.  The "envelope" is itself an email.  The body has text about the contents, and the "wrapped" email is included as an attachment.
	// The body may contain key details about the message known only to the mailserver that sent the journal, such as any Bcc recipients.  Therefore, we read the key metadata
	// from the body of the envelope

	// parse the provided input as an RFC2822 MIME-encoded email message
	env, err := mail.ReadMessage(in)
	if err != nil {
		return
	}

	// it parsed, so capture the timestamp from the envelope
	received, err := env.Header.Date()
	if err != nil {
		return nil, err
	}

	// it parsed, so parse the body as a MIME message
	b, err := enmime.ParseMIMEBody(env)
	if err != nil {
		return
	}
	// the body of the root message should contain metadata about the contents
	md, err := ParseBody(b.Text)
	if err != nil {
		return
	}
	// the envelope should contain one attachment that is the original message
	if len(b.OtherParts) != 1 {
		err = errors.New("Expected 1 MIME message in the envolope, but got " + strconv.Itoa(len(b.OtherParts)))
		return
	}
	r := bytes.NewReader(b.OtherParts[0].Content())
	m2, err := mail.ReadMessage(r)
	if err != nil {
		log.Fatalln(err)
	}

	// it parsed as a message, so let's process its contents
	b2, err := enmime.ParseMIMEBody(m2)
	if err != nil {
		return
	}

	// the first attachment should be the body of the mail in RTF format
	if len(b2.Attachments) < 1 {
		err = errors.New("Expected at least one attachment, but got " + strconv.Itoa(len(b.Attachments)))
	}

	// fire up an Apache Tika instance ot do the coversion to plain text
	t, err := tika.NewTika("http://localhost:9998/tika")
	if err != nil {
		return
	}

	data, err := t.Parse(bytes.NewBuffer(b2.Attachments[0].Content()), b2.Attachments[0].ContentType())
	if err != nil {
		return
	}
	md.JournalTimestamp = received
	m = &Message{
		Metadata: md,
		BodyText: string(data),
	}

	return
}

// ParseBody extracts key metadata about a journaled message from the envelope body
func ParseBody(b string) (m *Metadata, err error) {
	m = &Metadata{}
	scanner := bufio.NewScanner(strings.NewReader(b))
	for scanner.Scan() {
		k, v, ok := splitHeader(scanner.Text())
		if !ok {
			continue
		}
		switch k {
		case "Sender":
			m.Sender = strings.ToLower(v)
		case "On-Behalf-Of":
			m.OnBehalfOf = v
		case "Subject":
			m.Subject = v
		case "Message-Id":
			m.MessageID = strings.TrimSuffix(strings.TrimPrefix(v, "<"), ">")
		case "Recipient", "To":
			m.To = appendUniq(m.To, strings.Split(v, " Expanded: ")...)
		case "Cc":
			m.CC = appendUniq(m.CC, strings.Split(v, " Expanded: ")...)
		case "Bcc":
			m.BCC = appendUniq(m.BCC, strings.Split(v, " Expanded: ")...)
		default:
			log.Printf("Unhandled envelope header in envelope journal: %s\n", k)
		}
	}
	return
}

func splitHeader(h string) (k, v string, ok bool) {
	kv := strings.SplitN(h, ": ", 2)
	if len(kv) < 2 {
		return
	}
	return kv[0], kv[1], true
}

func appendUniq(start []string, more ...string) []string {
CheckExists:
	for _, m := range more {
		m = strings.ToLower(m)
		for _, s := range start {
			if m == s {
				continue CheckExists
			}
		}
		start = append(start, m)
	}
	return start
}
