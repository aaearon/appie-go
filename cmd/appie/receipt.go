package main

import (
	"context"
	"fmt"
	"strings"

	appie "github.com/gwillem/appie-go"
)

// trimMillis strips sub-second precision from a timestamp string (e.g. ".000" in "T14:30:00.000").
func trimMillis(s string) string {
	if before, _, ok := strings.Cut(s, "."); ok {
		return before
	}
	return s
}

type receiptCommand struct {
	Show receiptShowCommand `command:"show" description:"Show items for a receipt"`
	N    int                `short:"n" default:"20" description:"Number of recent receipts to show"`
}

func (cmd *receiptCommand) Execute(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unknown argument %q, did you mean: appie receipt show %s: %w", args[0], args[0], errBadArgs)
	}
	ctx, client, err := orderSetup()
	if err != nil {
		return err
	}
	return listReceipts(ctx, client, cmd.N)
}

func listReceipts(ctx context.Context, client *appie.Client, n int) error {
	receipts, err := client.GetReceipts(ctx)
	if err != nil {
		return fmt.Errorf("failed to get receipts: %w: %w", err, errUpstream)
	}

	limit := min(n, len(receipts))
	view := receipts[:limit]

	if globalOpts.JSON {
		return emitJSON(view, nil)
	}

	if len(view) == 0 {
		fmt.Println("No receipts found")
		return nil
	}
	for _, r := range view {
		fmt.Printf("%-20s %s %6.2f\n", r.TransactionID, trimMillis(r.Date), r.TotalAmount)
	}

	return nil
}

// show subcommand

type receiptShowCommand struct {
	Args struct {
		TransactionID string `positional-arg-name:"transaction-id" required:"true"`
	} `positional-args:"yes"`
}

func (cmd *receiptShowCommand) Execute(args []string) error {
	ctx, client, err := orderSetup()
	if err != nil {
		return err
	}
	return showReceipt(ctx, client, cmd.Args.TransactionID)
}

func showReceipt(ctx context.Context, client *appie.Client, id string) error {
	receipts, err := client.GetReceipts(ctx)
	if err != nil {
		return fmt.Errorf("failed to get receipts: %w: %w", err, errUpstream)
	}

	var meta *appie.Receipt
	for i, r := range receipts {
		if r.TransactionID == id {
			meta = &receipts[i]
			break
		}
	}

	receipt, err := client.GetReceipt(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get receipt: %w: %w", err, errUpstream)
	}

	warns := &Warnings{}
	// Copy Date from list metadata if detail call didn't populate it.
	if receipt.Date == "" && meta != nil {
		receipt.Date = meta.Date
	}
	// Only surface the missing-Date warning in JSON mode — text mode already
	// silently omits the Date line when meta is nil, and adding a stderr
	// warning here would be a behavior change for text consumers.
	if globalOpts.JSON && receipt.Date == "" {
		warnf(warns, "no metadata for receipt %s; Date unavailable", id)
	}

	if globalOpts.JSON {
		return emitJSON(receipt, warns.Slice())
	}

	fmt.Printf("Receipt %s\n", receipt.TransactionID)
	if meta != nil {
		fmt.Printf("Date:  %s\n", trimMillis(meta.Date))
	}
	fmt.Println()

	var subtotal float64
	for _, item := range receipt.Items {
		subtotal += item.Amount
		wi := ""
		if item.WebshopID > 0 {
			wi = fmt.Sprintf("  wi%d", item.WebshopID)
		}
		if item.Quantity > 1 {
			fmt.Printf("  %dx %-30s %6.2f%s\n", item.Quantity, item.Description, item.Amount, wi)
		} else {
			fmt.Printf("     %-30s %6.2f%s\n", item.Description, item.Amount, wi)
		}
	}

	if len(receipt.Discounts) > 0 {
		fmt.Println()
		for _, d := range receipt.Discounts {
			subtotal += d.Amount
			fmt.Printf("     %-30s %6.2f\n", d.Name, d.Amount)
		}
	}

	fmt.Printf("     %-30s ------\n", "")
	for _, p := range receipt.Payments {
		fmt.Printf("     %-30s %6.2f\n", p.Method, p.Amount)
	}

	return nil
}
