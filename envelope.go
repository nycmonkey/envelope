package envelope

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/mail"
	"strconv"
	"strings"
	"time"

	enmime "github.com/jhillyerd/go.enmime"
	sha256 "github.com/minio/sha256-simd"
)

// Metadata holds key details about a message from the body of a journal envelope
type Metadata struct {
	Sender, OnBehalfOf, Subject, MessageID string   `json:",omitempty"`
	To, CC, BCC                            []string `json:",omitempty"`
	Timestamp                              time.Time
}

// Part contains the key bits of a MIME part
type Part struct {
	FileName, ContentType, SHA256 string
	Content                       []byte
}

// Message holds the details of a journaled message
type Message struct {
	*Metadata
	SHA256 string
	Body   string
	Parts  []*Part
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

	md.Timestamp = received
	h := fmt.Sprintf("%x", sha256.Sum256(b.OtherParts[0].Content()))
	m = &Message{
		SHA256:   h,
		Metadata: md,
		Parts:    make([]*Part, 0),
	}

	partCollector := make(chan enmime.MIMEPart, 1)

	go func() {
		for p := range partCollector {
			m.Parts = append(m.Parts, &Part{FileName: p.FileName(), ContentType: p.ContentType(), Content: p.Content(), SHA256: fmt.Sprintf("%x", sha256.Sum256(p.Content()))})
		}
	}()
	extractParts(b.OtherParts[0], partCollector)
	close(partCollector)
	return
}

func extractParts(part enmime.MIMEPart, ch chan enmime.MIMEPart) {
	for _, p := range enmime.DepthMatchAll(part, func(part enmime.MIMEPart) bool { return true }) {
		switch p.ContentType() {
		case `message/rfc822`:
			m, err := mail.ReadMessage(bytes.NewReader(p.Content()))
			if err != nil {
				log.Fatalln("reading message/rfc822 in extractParts:", err)
			}
			p2, err := enmime.ParseMIMEBody(m)
			if err != nil {
				log.Println("enmime.ParseBODY in extractParts:", err)
				continue
			}
			extractParts(p2.Root, ch)
		case "multipart/mixed":
			continue
		default:
			ch <- p
		}
	}
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
			if strings.Contains(v, ", Forwarded: ") {
				m.To = appendUniq(m.To, strings.Split(v, ", Forwarded: ")...)
			} else {
				m.To = appendUniq(m.To, strings.Split(v, ", Expanded: ")...)
			}
		case "Cc":
			if strings.Contains(v, ", Forwarded: ") {
				m.CC = appendUniq(m.CC, strings.Split(v, ", Forwarded: ")...)
			} else {
				m.CC = appendUniq(m.CC, strings.Split(v, ", Expanded: ")...)
			}
		case "Bcc":
			if strings.Contains(v, ", Forwarded: ") {
				m.CC = appendUniq(m.CC, strings.Split(v, ", Forwarded: ")...)
			} else {
				m.BCC = appendUniq(m.BCC, strings.Split(v, ", Expanded: ")...)
			}

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
