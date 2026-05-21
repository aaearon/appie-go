package main

import (
	"fmt"

	appie "github.com/gwillem/appie-go"
)

type productCommand struct {
	Args struct {
		IDs []int `positional-arg-name:"id" required:"1"`
	} `positional-args:"yes"`
	Detail bool `short:"d" long:"detail" description:"Show full detail (incl. nutrition) for each product"`
}

func (cmd *productCommand) Execute(args []string) error {
	ctx, client, err := orderSetup()
	if err != nil {
		return err
	}

	ids := cmd.Args.IDs

	products, err := client.GetProductsByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("get products failed: %w: %w", err, errUpstream)
	}

	warns := &Warnings{}
	for _, id := range missingIDs(ids, products) {
		warnf(warns, "product %d not found", id)
	}

	if len(products) == 0 {
		return fmt.Errorf("no products resolved: %w", errNotFound)
	}

	if !cmd.Detail {
		if globalOpts.JSON {
			return emitJSON(products, warns.Slice())
		}
		printProducts(products)
		return nil
	}

	full := make([]*appie.Product, 0, len(products))
	for i, p := range products {
		got, err := client.GetProductFull(ctx, p.ID)
		if err != nil {
			warnf(warns, "nutrition fetch for %d failed: %v", p.ID, err)
			full = append(full, &products[i])
			if !globalOpts.JSON {
				if i > 0 {
					fmt.Println()
				}
				printProductDetail(&p)
			}
			continue
		}
		full = append(full, got)
		if !globalOpts.JSON {
			if i > 0 {
				fmt.Println()
			}
			printProductDetail(got)
		}
	}
	if globalOpts.JSON {
		return emitJSON(full, warns.Slice())
	}
	return nil
}

// formatBonus renders a bonus mechanism, appending the validity date range
// when both dates are available. Returns "" for an empty mechanism so callers
// can skip the line entirely.
func formatBonus(mechanism, startDate, endDate string) string {
	if mechanism == "" {
		return ""
	}
	if startDate != "" && endDate != "" {
		return fmt.Sprintf("%s (%s → %s)", mechanism, startDate, endDate)
	}
	return mechanism
}

// missingIDs returns the input IDs that are not present in products.
// Order matches the input order; duplicates in input are reported once each.
func missingIDs(ids []int, products []appie.Product) []int {
	got := make(map[int]struct{}, len(products))
	for _, p := range products {
		got[p.ID] = struct{}{}
	}
	var missing []int
	for _, id := range ids {
		if _, ok := got[id]; !ok {
			missing = append(missing, id)
		}
	}
	return missing
}

func printProductDetail(p *appie.Product) {
	fmt.Printf("%s\n", p.Title)
	if p.Brand != "" {
		fmt.Printf("  Brand:       %s\n", p.Brand)
	}
	fmt.Printf("  ID:          %d\n", p.ID)
	if p.UnitSize != "" {
		fmt.Printf("  Unit size:   %s\n", p.UnitSize)
	}
	if p.Price.Was > 0 && p.Price.Was != p.Price.Now {
		fmt.Printf("  Price:       €%.2f (was €%.2f)\n", p.Price.Now, p.Price.Was)
	} else {
		fmt.Printf("  Price:       €%.2f\n", p.Price.Now)
	}
	if p.UnitPriceDescription != "" {
		fmt.Printf("  Unit price:  %s\n", p.UnitPriceDescription)
	}
	if bonus := formatBonus(p.BonusMechanism, p.BonusStartDate, p.BonusEndDate); bonus != "" {
		fmt.Printf("  Bonus:       %s\n", bonus)
	}
	if p.Category != "" {
		cat := p.Category
		if p.SubCategory != "" {
			cat += " / " + p.SubCategory
		}
		fmt.Printf("  Category:    %s\n", cat)
	}
	if p.NutriScore != "" {
		fmt.Printf("  Nutri-Score: %s\n", p.NutriScore)
	}
	fmt.Printf("  Available:   %t\n", p.IsAvailable)
	fmt.Printf("  Orderable:   %t\n", p.IsOrderable)
	if p.ShortDescription != "" {
		fmt.Printf("\n  %s\n", p.ShortDescription)
	}
	if len(p.NutritionalInfo) > 0 {
		fmt.Println("\n  Nutrition:")
		var lastPer string
		headerPrinted := false
		for _, n := range p.NutritionalInfo {
			if !headerPrinted || n.Per != lastPer {
				if headerPrinted {
					fmt.Println()
				}
				header := n.Per
				if header == "" {
					header = "(unspecified basis)"
				} else {
					header = "per " + header
				}
				fmt.Printf("    %s:\n", header)
				lastPer = n.Per
				headerPrinted = true
			}
			fmt.Printf("      %-30s %s\n", n.Name, n.Value)
		}
	}
}
