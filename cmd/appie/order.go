package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"text/tabwriter"

	appie "github.com/gwillem/appie-go"
)

type orderCommand struct {
	Show orderShowCommand `command:"show" description:"Show contents of an order"`
	Add  orderAddCommand  `command:"add" description:"Add a product to an order"`
	Rm   orderRmCommand   `command:"rm" description:"Remove a product from an order"`
}

func (cmd *orderCommand) Execute(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unknown argument %q, did you mean: appie order show %s: %w", args[0], args[0], errBadArgs)
	}
	ctx, client, err := orderSetup()
	if err != nil {
		return err
	}

	fulfillments, err := client.GetFulfillments(ctx)
	if err != nil {
		return fmt.Errorf("failed to get orders: %w: %w", err, errUpstream)
	}

	if globalOpts.JSON {
		return emitJSON(fulfillments, nil)
	}

	if len(fulfillments) == 0 {
		fmt.Println("No open orders")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
	fmt.Fprintf(w, "\t%s\t%s\t%s\t%s\t\n", "Order", "Status", "Delivery", "Total")
	for _, f := range fulfillments {
		delivery := f.Delivery.Slot.DateDisplay
		if f.Delivery.Slot.TimeDisplay != "" {
			delivery += "  " + f.Delivery.Slot.TimeDisplay
		}
		fmt.Fprintf(w, "\t%d\t%s\t%s\t%.2f\t\n", f.OrderID, f.Status, delivery, f.TotalPrice)
	}
	return w.Flush()
}

func findFulfillment(fulfillments []appie.Fulfillment, orderID string) *appie.Fulfillment {
	for i, f := range fulfillments {
		if strconv.Itoa(f.OrderID) == orderID {
			return &fulfillments[i]
		}
	}
	return nil
}

// ensureOrderOpen finds the fulfillment for orderID, validates it exists,
// reopens the order if SUBMITTED/CONFIRMED, and sets the client's active order ID.
func ensureOrderOpen(ctx context.Context, client *appie.Client, fulfillments []appie.Fulfillment, orderID int, warns *Warnings) error {
	var found *appie.Fulfillment
	for i, f := range fulfillments {
		if f.OrderID == orderID {
			found = &fulfillments[i]
			break
		}
	}

	if found == nil {
		return fmt.Errorf("order %d not found in open orders: %w", orderID, errNotFound)
	}

	if found.Status == "SUBMITTED" || found.Status == "CONFIRMED" {
		if err := client.ReopenOrder(ctx, orderID); err != nil {
			return fmt.Errorf("failed to reopen order: %w: %w", err, errUpstream)
		}
		progress(warns, "Reopened order %d (was %s)", orderID, found.Status)
	}

	client.SetOrderID(orderID)
	return nil
}

func printOrder(order *appie.Order, f *appie.Fulfillment) error {
	fmt.Printf("Order %s  %s\n", order.ID, order.State)

	if f != nil {
		delivery := f.Delivery.Slot.DateDisplay
		if f.Delivery.Slot.TimeDisplay != "" {
			delivery += "  " + f.Delivery.Slot.TimeDisplay
		}
		fmt.Printf("Delivery: %s\n", delivery)
	}
	fmt.Println()

	if len(order.Items) == 0 {
		fmt.Println("No items")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, item := range order.Items {
		// Always show undiscounted price per line
		unitPrice := item.Product.Price.Now
		if item.Product.Price.Was > 0 {
			unitPrice = item.Product.Price.Was
		}
		linePrice := float64(item.Quantity) * unitPrice

		bonus := ""
		if item.Product.BonusMechanism != "" {
			bonus = "  " + item.Product.BonusMechanism
		}
		fmt.Fprintf(w, "  %d\t%s\t%s\t%d\t%6.2f%s\n", item.ProductID, item.Product.Title, item.Product.UnitSize, item.Quantity, linePrice, bonus)
	}

	// Use API-provided totals (from order summary or fulfillment)
	total := order.TotalPrice
	discount := order.TotalDiscount
	if total == 0 && f != nil && f.TotalPrice > 0 {
		total = f.TotalPrice
		subtotal := order.Subtotal()
		if subtotal > total {
			discount = subtotal - total
		}
	}

	fmt.Fprintf(w, "\t\t\t\t──────\n")
	if discount > 0 {
		fmt.Fprintf(w, "\t\t\t\t-%5.2f  bonus\n", discount)
	}
	fmt.Fprintf(w, "\t\t\t%d items\t%6.2f\n", len(order.Items), total)
	return w.Flush()
}

// show subcommand

type orderShowCommand struct {
	Args struct {
		OrderID int `positional-arg-name:"order-id" required:"true"`
	} `positional-args:"yes"`
}

func (cmd *orderShowCommand) Execute(args []string) error {
	ctx, client, err := orderSetup()
	if err != nil {
		return err
	}

	fulfillments, err := client.GetFulfillments(ctx)
	if err != nil {
		return fmt.Errorf("failed to get orders: %w: %w", err, errUpstream)
	}

	orderID := cmd.Args.OrderID

	order, err := client.GetOrderDetails(ctx, orderID)
	if err != nil {
		return fmt.Errorf("failed to get order details: %w: %w", err, errUpstream)
	}

	// Try to get summary for totals
	client.SetOrderID(orderID)
	if summary, err := client.GetOrder(ctx); err == nil {
		order.TotalPrice = summary.TotalPrice
		order.TotalDiscount = summary.TotalDiscount
	}

	f := findFulfillment(fulfillments, order.ID)

	if globalOpts.JSON {
		return emitJSON(map[string]any{
			"order":       order,
			"fulfillment": f,
		}, nil)
	}
	return printOrder(order, f)
}

// add subcommand

type orderAddCommand struct {
	Args struct {
		OrderID int    `positional-arg-name:"order-id" required:"true"`
		Product string `positional-arg-name:"product" required:"true"`
	} `positional-args:"yes"`
	Quantity int `short:"n" long:"quantity" default:"1" description:"Quantity to add"`
}

func (cmd *orderAddCommand) Execute(args []string) error {
	ctx, client, err := orderSetup()
	if err != nil {
		return err
	}

	fulfillments, err := client.GetFulfillments(ctx)
	if err != nil {
		return fmt.Errorf("failed to get orders: %w: %w", err, errUpstream)
	}

	orderID := cmd.Args.OrderID
	warns := &Warnings{}
	if err := ensureOrderOpen(ctx, client, fulfillments, orderID, warns); err != nil {
		return err
	}

	product := cmd.Args.Product
	qty := cmd.Quantity

	// If numeric, use as product ID directly
	productID, err := strconv.Atoi(product)
	if err != nil {
		// Search for the product
		products, err := client.SearchProducts(ctx, product, 15)
		if err != nil {
			return fmt.Errorf("search failed: %w: %w", err, errUpstream)
		}
		if len(products) == 0 {
			return fmt.Errorf("no products found for %q: %w", product, errNotFound)
		}
		if len(products) > 1 {
			if globalOpts.JSON {
				cands := make([]map[string]any, len(products))
				for i, p := range products {
					cands[i] = map[string]any{"id": p.ID, "title": p.Title}
				}
				return newAmbiguous(
					fmt.Sprintf("multiple matches for %q, specify product ID", product),
					cands,
				)
			}
			printProducts(products)
			return fmt.Errorf("multiple matches for %q, specify product ID: %w", product, errAmbiguous)
		}
		productID = products[0].ID
		progress(warns, "Found: %s", products[0].Title)
	}

	if err := client.AddToOrder(ctx, []appie.OrderItem{{ProductID: productID, Quantity: qty}}); err != nil {
		return fmt.Errorf("add to order failed: %w: %w", err, errUpstream)
	}

	if globalOpts.JSON {
		return emitJSON(map[string]any{
			"action":    "order_add",
			"orderId":   orderID,
			"productId": productID,
			"quantity":  qty,
		}, warns.Slice())
	}
	fmt.Printf("Added %dx %d to order %d\n", qty, productID, orderID)
	return nil
}

// rm subcommand

type orderRmCommand struct {
	Args struct {
		OrderID   int `positional-arg-name:"order-id" required:"true"`
		ProductID int `positional-arg-name:"product-id" required:"true"`
	} `positional-args:"yes"`
}

func (cmd *orderRmCommand) Execute(args []string) error {
	ctx, client, err := orderSetup()
	if err != nil {
		return err
	}

	fulfillments, err := client.GetFulfillments(ctx)
	if err != nil {
		return fmt.Errorf("failed to get orders: %w: %w", err, errUpstream)
	}

	orderID := cmd.Args.OrderID
	warns := &Warnings{}
	if err := ensureOrderOpen(ctx, client, fulfillments, orderID, warns); err != nil {
		return err
	}

	productID := cmd.Args.ProductID
	if err := client.RemoveFromOrder(ctx, productID); err != nil {
		return fmt.Errorf("remove from order failed: %w: %w", err, errUpstream)
	}

	if globalOpts.JSON {
		return emitJSON(map[string]any{
			"action":    "order_rm",
			"orderId":   orderID,
			"productId": productID,
		}, warns.Slice())
	}
	fmt.Printf("Removed %d from order %d\n", productID, orderID)
	return nil
}

// clientFactory builds the authenticated client used by every command's
// orderSetup() call. Tests can swap this to point at an httptest server.
var clientFactory = func() (*appie.Client, error) {
	client, err := appie.NewWithConfig(globalOpts.Config, clientOpts()...)
	if err != nil {
		// Config-file problems are user/config errors (bad path, invalid
		// JSON, perms), not auth failures — classify accordingly.
		return nil, fmt.Errorf("failed to load config: %w: %w", err, errBadConfig)
	}
	if !client.IsAuthenticated() {
		return nil, fmt.Errorf("not authenticated, run 'appie login' first: %w", errAuth)
	}
	return client, nil
}

// orderSetup creates an authenticated client and context.
func orderSetup() (context.Context, *appie.Client, error) {
	client, err := clientFactory()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	_ = cancel // cleaned up when process exits
	return ctx, client, nil
}
