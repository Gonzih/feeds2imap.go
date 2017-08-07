package main

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"strings"
	"time"

	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/mail"
	"github.com/mmcdole/gofeed"
	"github.com/spf13/viper"
)

var mailTemplate = template.Must(template.New("mail").Parse(
	`<table>
<tbody>
<tr><td>
<a href="{{ .Link }}">{{ .Title }}</a> {{ .Author }}
<hr>
</td></tr>
<tr><td>
{{ .Content }}
</td></tr>
</tbody>
</table>`))

type templatePayload struct {
	Link    string
	Title   string
	Author  string
	Content template.HTML
}

func FormatContent(item *gofeed.Item) (string, error) {
	var payload templatePayload

	payload.Link = item.Link
	payload.Title = item.Title

	if item.Author != nil {
		payload.Author = fmt.Sprintf("%s %s", item.Author.Name, item.Author.Email)
	}

	if len(item.Content) > 0 {
		payload.Content = template.HTML(item.Content)
	} else {
		payload.Content = template.HTML(item.Description)
	}

	var buffer bytes.Buffer

	err := mailTemplate.Execute(&buffer, payload)

	if err != nil {
		return "", err
	}

	return buffer.String(), nil
}

func NewMessage(item *gofeed.Item) (bytes.Buffer, error) {
	var b bytes.Buffer

	fromName := viper.GetString("imap.from.name")

	if item.Author != nil {
		fromName = fmt.Sprintf("%s %s", item.Author.Name, item.Author.Email)
	}

	from := []*mail.Address{{fromName, viper.GetString("imap.from.email")}}
	to := []*mail.Address{{viper.GetString("imap.to.name"), viper.GetString("imap.to.email")}}

	mediaParams := map[string]string{"charset": "utf-8"}

	h := mail.NewHeader()
	h.SetContentType("multipart/alternative", mediaParams)
	h.SetDate(*item.PublishedParsed)
	h.SetAddressList("From", from)
	h.SetAddressList("To", to)
	h.SetSubject(item.Title)

	messageWriter, err := mail.CreateWriter(&b, h)
	defer messageWriter.Close()
	if err != nil {
		return b, err
	}

	htmlHeader := mail.NewTextHeader()
	htmlHeader.SetContentType("text/html", mediaParams)
	htmlWriter, err := messageWriter.CreateSingleText(htmlHeader)
	defer htmlWriter.Close()
	if err != nil {
		return b, err
	}

	content, err := FormatContent(item)
	if err != nil {
		return b, err
	}

	io.WriteString(htmlWriter, content)

	return b, nil
}

func newIMAPClient() (*client.Client, error) {
	hostPort := fmt.Sprintf("%s:%d", viper.GetString("imap.host"), viper.GetInt("imap.port"))
	c, err := client.DialTLS(hostPort, nil)
	if err != nil {
		return c, err
	}

	if err := c.Login(viper.GetString("imap.username"), viper.GetString("imap.password")); err != nil {
		return c, err
	}

	if viper.GetBool("debug") {
		log.Println("Logged in to IMAP")
	}

	return c, nil
}

func AppendNewItemsViaIMAP(items ItemsWithFolders) error {
	if viper.GetBool("debug") {
		log.Printf("Found %d new items", len(items))
	}

	client, err := newIMAPClient()
	if err != nil {
		return err
	}
	defer client.Logout()

	for _, entry := range items {
		if entry.Item.PublishedParsed == nil {
			t := time.Now()
			entry.Item.PublishedParsed = &t
		}

		folderName := entry.Folder
		if viper.GetBool("imap.folder_capitalize") {
			folderName = strings.Title(folderName)
		}
		folder := fmt.Sprintf("%s/%s", viper.GetString("imap.folder_prefix"), folderName)

		_ = client.Create(folder)

		msg, err := NewMessage(entry.Item)
		if err != nil {
			return err
		}

		if viper.GetBool("debug") {
			log.Printf("Appending item to %s", folder)
		}

		literal := bytes.NewReader(msg.Bytes())
		err = client.Append(folder, []string{}, *entry.Item.PublishedParsed, literal)
		if err != nil {
			return err
		}
	}

	return nil
}
