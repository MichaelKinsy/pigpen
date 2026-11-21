;;;; autolith-bridge: a pig subprocess extension scaffolded by `pig extension init`.
;;;; Pig builds this system into a cached SBCL image. The pig-sdk system is the staged SDK.

(asdf:defsystem "autolith-bridge"
  :depends-on ("pig-sdk")
  :serial t
  :components ((:file "autolith-bridge")))
