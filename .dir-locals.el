;; set goroot
((go-mode . ((eval . (progn
                       (let ((project-root (expand-file-name (locate-dominating-file buffer-file-name ".dir-locals.el"))))
                         (let ((project-new-root (string-remove-suffix "/" project-root)))
                           (let ((goroot (format "%s" project-new-root)))
                             (setenv "GOROOT" goroot)))))))))
