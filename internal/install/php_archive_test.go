package install

import "testing"

func TestNewestArchivedPicksTheLastPatchOfTheBranch(t *testing.T) {
	listing := `<a href="php-7.2.9-nts-Win32-VC15-x64.zip">x</a> <a href="php-7.2.34-nts-Win32-VC15-x64.zip">x</a>
<a href="php-7.2.34-Win32-VC15-x64.zip">x</a> <a href="php-7.2.34-nts-Win32-VC15-x86.zip">x</a>
<a href="php-7.3.33-nts-Win32-VC15-x64.zip">x</a> <a href="php-8.1.34-nts-Win32-vs16-x64.zip">x</a>`
	if file, v := newestArchived(listing, "7.2"); file != "php-7.2.34-nts-Win32-VC15-x64.zip" || v != "7.2.34" {
		t.Fatalf("7.2: %s %s", file, v)
	}
	if file, v := newestArchived(listing, "8.1"); file != "php-8.1.34-nts-Win32-vs16-x64.zip" || v != "8.1.34" {
		t.Fatalf("8.1: %s %s", file, v)
	}
	if file, _ := newestArchived(listing, "5.6"); file != "" {
		t.Fatalf("5.6 is not in the listing, got %s", file)
	}
}

func TestSumForReadsSha256sumTxt(t *testing.T) {
	sums := "14ae3250d4447c8ccfc4c45a70d90adfbcd61e728d85f0be56a7ddf8f9c8aace *php-7.4.33-nts-Win32-vc15-x64.zip\r\n" +
		"9cfe246cb144076c16f5913a3ef88a474c3dd7e60f0f0c8bb95faf68674016cc *php-8.1.34-nts-Win32-vs16-x64.zip\r\n"
	if got := sumFor(sums, "php-8.1.34-nts-Win32-vs16-x64.zip"); got != "9cfe246cb144076c16f5913a3ef88a474c3dd7e60f0f0c8bb95faf68674016cc" {
		t.Fatalf("got %q", got)
	}
	if got := sumFor(sums, "php-7.2.34-nts-Win32-VC15-x64.zip"); got != "" {
		t.Fatalf("7.2.34 has no published sum, got %q", got)
	}
}
