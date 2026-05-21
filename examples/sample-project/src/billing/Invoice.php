<?php

namespace App\Billing;

use Lattice\Attributes\{Feature, Capability, EnforcesInvariant};

#[Feature('billing')]
#[Capability('invoice_generation')]
final class Invoice
{
    /**
     * Sum the line items. The total is defined as that sum, so INV-1
     * (total equals the sum of line items) holds by construction.
     */
    #[EnforcesInvariant('INV-1')]
    public function total(array $lineItems): int
    {
        $sum = 0;
        foreach ($lineItems as $item) {
            $sum += $item;
        }
        return $sum;
    }
}
