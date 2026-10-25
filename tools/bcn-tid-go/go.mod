module maryskelter-unburger/bcn-tid-go

go 1.25.0

require (
	github.com/woozymasta/bcn v0.7.0
	maryskelter-unburger v0.0.0
)

require golang.org/x/sys v0.47.0 // indirect

replace maryskelter-unburger => ../..

replace github.com/woozymasta/bcn => ../../third_party/go/bcn

replace golang.org/x/sys => ../../third_party/go/xsys
