.PHONY: css run

# builds static/dist/app.css; commit it. .paddock links the kit's folder (the module cache, or
# the replace line's directory), for daisyUI, the kit's pages and the Tailwind installer.
css:
	ln -sfn "$$(go list -m -f '{{.Dir}}' github.com/rbxnk/paddock)" .paddock
	sh .paddock/scripts/tailwind.sh
	.bin/tailwindcss -i css/main.css -o static/dist/app.css --minify

run: css
	go tool templ generate
	go run . --dev --db data/__NAME__.db serve
