<?php

namespace App\Tests;

use Lattice\Attributes\Verifies;
use App\Billing\Invoice;

final class InvoiceTest
{
    #[Verifies('billing:INV-1')]
    public function testTotalEqualsSumOfLineItems(): void
    {
        $invoice = new Invoice();
        $total = $invoice->total([10, 20, 30]);
        assert($total === 60);
    }
}
