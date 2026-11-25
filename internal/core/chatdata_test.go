package core

import (
	"fmt"
	"strings"
	"testing"

	"codegen/internal/chat"
	"codegen/internal/flow"
)

// Large setup data goes to the AI as a catalog; it queries the tables and
// searches the documents with the built-in tools, and the passages that
// match a question come with it.
func TestChatQueriesAndSearchesItsData(t *testing.T) {
	h, ws := flowHub(t)
	ag := &chatScript{replies: []string{
		"```specwright-run\n{\"tool\": \"data.query\", \"args\": {\"sql\": \"SELECT region, sum(amount) AS total FROM setup_sales GROUP BY region ORDER BY total DESC\"}}\n```\n" +
			"```specwright-run\n{\"tool\": \"data.search\", \"args\": {\"query\": \"warranty claims\"}}\n```\n" +
			"```specwright-run\n{\"tool\": \"data.query\", \"args\": {\"sql\": \"DROP TABLE setup_sales\"}}\n```",
		"EU sold the most.",
	}}
	h.Agents.Add(ag)
	var csv strings.Builder
	csv.WriteString("region,amount,product\n")
	for i := 0; i < 1500; i++ {
		fmt.Fprintf(&csv, "%s,%d,widget-%d\n", []string{"US", "EU", "APAC"}[i%3], 10+i%3*5, i)
	}
	csv.WriteString("US,1,LAST-ROW-MARKER\n")
	var doc strings.Builder
	doc.WriteString("# Handbook\n\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&doc, "## Topic %d\n\n%s\n\n", i, strings.Repeat("General notes about this topic, filling the handbook with ordinary words. ", 6))
		if i == 20 {
			doc.WriteString("## Returns\n\nA customer may return an order within 30 days for a full refund.\n\n")
		}
		if i == 40 {
			doc.WriteString("## Warranty\n\nWarranty claims go to the service desk with the receipt.\n\n")
		}
	}
	f := flow.New("shop-chat", "Shop chat")
	f.Nodes = append(f.Nodes,
		flow.Node{ID: "sales", Type: "transform", Op: "template", Template: csv.String(), Title: "Sales rows"},
		flow.Node{ID: "docs", Type: "transform", Op: "template", Template: doc.String(), Title: "Handbook"},
		flow.Node{ID: "chat", Type: "chat", Instructions: "Answer."})
	f.Edges = []flow.Edge{{From: "start", To: "sales"}, {From: "start", To: "docs"}, {From: "sales", To: "chat"}, {From: "docs", To: "chat"}}
	if err := flow.Save(ws, f); err != nil {
		t.Fatal(err)
	}
	s, err := h.StartChatSession("shop-chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitChat(t, h, "shop-chat", s.ID, func(x chat.Session) bool { return x.Status == "ready" })
	if _, err := h.SendChatMessage("shop-chat", s.ID, "How do returns work, and which region sells most?", nil); err != nil {
		t.Fatal(err)
	}
	ms := waitAnswer(t, h, "shop-chat", s.ID, 2)
	a := ms[len(ms)-1]
	if a.Text != "EU sold the most." || a.Error != "" {
		t.Fatalf("answer %+v", a)
	}

	first := ag.reqs[0].Prompt
	for _, want := range []string{
		"table setup_sales, 1501 rows", "columns: region TEXT, amount INTEGER, product TEXT", "first rows:", // the catalog
		`tool "data.query"`, `tool "data.search"`,
		"PASSAGES OF THE SESSION'S DOCUMENTS", "full refund", // the passage that matches the question
	} {
		if !strings.Contains(first, want) {
			t.Errorf("the first prompt has no %q", want)
		}
	}
	if strings.Contains(first, "LAST-ROW-MARKER") || strings.Contains(first, "Warranty claims go") {
		t.Error("the large data was sent whole")
	}
	if !strings.Contains(first, "passages — too large to send") {
		t.Error("the document's catalog entry is missing")
	}

	if len(a.Steps) != 3 {
		t.Fatalf("steps %+v", a.Steps)
	}
	q, sr, bad := a.Steps[0], a.Steps[1], a.Steps[2]
	if q.Tool != "data.query" || q.Error != "" || q.Data == "" || sr.Tool != "data.search" || sr.Error != "" || bad.Error == "" {
		t.Fatalf("steps %+v", a.Steps)
	}
	out, _ := h.ChatData("shop-chat", s.ID, q.Data)
	if out != "region,total\nAPAC,10000\nEU,7500\nUS,5001\n" {
		t.Errorf("the query's result %q", out)
	}
	back := ag.reqs[1].Prompt
	if !ag.reqs[1].Resume || !strings.Contains(back, "APAC,10000") || !strings.Contains(back, "Warranty claims go to the service desk") || !strings.Contains(back, "only a SELECT") {
		t.Errorf("the results that went back:\n%s", back)
	}
	// the query's result is not indexed as more data
	st := chat.New(ws)
	x, err := st.OpenIndex("shop-chat", s.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	cat, _ := x.Catalog()
	if len(cat) != 2 {
		t.Errorf("catalog %+v", cat)
	}
}
