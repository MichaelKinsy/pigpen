;;;; Offline checks of the event reader and the run state: sbcl --script tests/parser-tests.lisp <path to the staged pig-sdk.asd> (no Autolith, no model).
(require :asdf)
(asdf:load-asd (truename (car (last sb-ext:*posix-argv*))))
(asdf:load-system "pig-sdk")
(load (merge-pathnames "../autolith-bridge.lisp" *load-truename*))
(in-package "autolith-bridge")

(defvar *failures* 0)
(defmacro check (what form) `(unless ,form (incf *failures*) (format t "FAIL ~A~%" ,what)))

(let* ((text (slurp (merge-pathnames "sample-events.sexp" *load-truename*))) (run (make-run :id "t")))
  (multiple-value-bind (forms end) (read-data-forms text 0)
    (check "14 records, including the multi-line one" (= (length forms) 14))
    (check "all consumed" (>= end (length (string-right-trim '(#\Newline) text))))
    (dolist (f forms) (apply-event run f))
    (check "final state" (equal (run-state run) "succeeded"))
    (check "seq" (= (run-seq run) 13))
    (check "tools, a finish reported twice counted once" (and (= (run-tools-started run) 2) (= (run-tools-finished run) 2) (= (run-tools-failed run) 1)))
    (check "progress counted" (= (run-progress run) 1))
    (check "omitted" (= (run-omitted run) 3))
    (check "usage adds tokens per request, counts requests once" (and (= (run-total run 0) 4100) (= (run-total run 1) 390) (= (run-total run 2) 2)))
    (check "no tool left running" (null (run-tool run)))))

;; a record still being written is left for the next read, then completes
(let* ((a "(:AUTOLITH-EVENT :VERSION 1 :SEQ 1 :KIND :RUN-STARTED :DATA NIL)") (b "(:AUTOLITH-EVENT :VERSION 1 :SEQ 2 :KIND :JOB-STARTED :DATA (:JOB-ID \"x\")")
       (partial (format nil "~A~%~A" a b)))
  (multiple-value-bind (forms end) (read-data-forms partial 0)
    (check "one complete form" (= (length forms) 1))
    (multiple-value-bind (more end2) (read-data-forms (concatenate 'string partial ")" (string #\Newline)) end)
      (check "the rest completes" (and (= (length more) 1) (> end2 end))))))

;; *read-eval* is off: a hostile record is an error, never evaluated
(check "#. refused" (handler-case (progn (read-data-forms "#.(error \"evaluated\")" 0) nil) (error (e) (not (search "evaluated" (princ-to-string e))))))

;; a primary session's conversation file (a real one, from the run that redefined knowledge-format-duration): tool results, usage, the answer
(let* ((run (make-run :id "s")) (text (slurp (merge-pathnames "sample-session.sexp" *load-truename*))))
  (funcall (file-poller (lambda () text) (lambda (record) (apply-record run record))))
  (check "session: four tools started and finished" (and (= (run-tools-started run) 4) (= (run-tools-finished run) 4) (= (run-tools-failed run) 0)))
  (check "session: five requests" (= (run-total run 2) 5))
  (check "session: tool results noted in order" (and (= (length (run-notes run)) 4) (search "self.redefine ok" (second (run-notes run))) (search "self.exercise ok" (third (run-notes run))) (search "self.commit ok" (fourth (run-notes run)))))
  (check "session: the answer is the last assistant text" (and (stringp (run-answer run)) (search "private image commit" (run-answer run))))
  (check "session id from the detached start line" (equal (session-id (format nil "Conversation 3-2XYLzs  PID 3654750~%")) "32XYLzs")))

;; the job file: keywords and NIL unqualified (Autolith rejects "COMMON-LISP:NIL"), and it reads back as the same job
(let ((path "/tmp/autolith-bridge-test-job.sexp"))
  (write-job path "j1" "task" (format nil "fix \"it\"~%now") 120)
  (let* ((text (slurp path)) (job (first (read-data-forms text 0))))
    (check "no COMMON-LISP: prefix" (not (search "COMMON-LISP:" text)))
    (check "prompt round-trips" (equal (getf (rest job) :prompt) (format nil "fix \"it\"~%now")))
    (check ":input NIL present" (and (member :input (rest job)) (null (getf (rest job) :input))))
    (check "contract kept" (equal (getf (rest job) :output-contract) *contract*)))
  (delete-file path))

;; the result envelope
(let ((r (first (read-data-forms "(:AUTOLITH-JOB-RESULT :VERSION 1 :ID \"j\" :STATUS :SUCCEEDED :RESULT (:OBJECT (\"summary\" \"did it\") (\"evidence\" \"line 1
line 2\")))" 0))))
  (let ((out (outcome (make-run :id "j") nil r "" nil)))
    (check "success text" (search "summary:" (gethash "content" out)))
    (check "multi-line string kept" (search "line 1" (gethash "content" out)))))
(let ((r (first (read-data-forms "(:AUTOLITH-JOB-RESULT :VERSION 1 :ID \"j\" :STATUS :TIMED-OUT :FAILURE (:CATEGORY :TIMEOUT :MESSAGE \"The job exceeded 5 seconds.\"))" 0))))
  (let ((out (outcome (make-run :id "j") nil r "" nil)))
    (check "failure is an error with category and status" (and (eq (gethash "is_error" out) t) (search "timed-out" (gethash "content" out)) (search "category timeout" (gethash "content" out))))))

;; validate-temp-dir rejects empty, relative or failing mktemp output before unwind-protect can delete anything
(check "validate-temp-dir accepts absolute dir" (equal (validate-temp-dir (pig::make-exec-result (format nil "/tmp/autolith-bridge.abc~%") "" 0 nil nil)) "/tmp/autolith-bridge.abc"))
(check "validate-temp-dir rejects empty stdout" (handler-case (progn (validate-temp-dir (pig::make-exec-result "" "" 0 nil nil)) nil) (error () t)))
(check "validate-temp-dir rejects relative path" (handler-case (progn (validate-temp-dir (pig::make-exec-result "rel/dir" "" 0 nil nil)) nil) (error () t)))
(check "validate-temp-dir rejects nonzero exit" (handler-case (progn (validate-temp-dir (pig::make-exec-result "/tmp/autolith-bridge.abc" "err" 1 nil nil)) nil) (error () t)))

;; apply-record caps single-line tool output notes at 160 characters
(let ((run (make-run :id "cap"))
      (long (make-string 500 :initial-element #\a)))
  (apply-record run `(:tool-result :tool "bash" :status :ok :output ,long))
  (check "note capped at 160 chars when no newline" (= (length (first (run-notes run))) (+ (length "bash ok: ") 160))))

;; file-poller isolates a signaling record so later records in the same read still apply
(let ((seen '()) (errs 0))
  (funcall (file-poller (lambda () "(:a) (:boom) (:b)")
                        (lambda (form) (if (eq (first form) :boom) (error "bad record") (push (first form) seen)))
                        (lambda (e) (declare (ignore e)) (incf errs))))
  (check "file-poller applies records after a failing one" (and (equal (nreverse seen) '(:a :b)) (= errs 1))))

;; Autolith version parsing and CLI style selection (top-level >= 0.67.47 vs legacy localgroup < 0.67.47)
(check "parse-autolith-version parses clingon output" (equal (parse-autolith-version (format nil "autolith version 0.67.66~%")) '(0 67 66)))
(check "parse-autolith-version parses dev suffix" (equal (parse-autolith-version "autolith version 0.67.47-dev.2") '(0 67 47)))
(check "parse-autolith-version returns nil on empty" (null (parse-autolith-version "")))
(check "cli-style-for-version: 0.67.24 -> :localgroup" (eq (cli-style-for-version "autolith version 0.67.24") :localgroup))
(check "cli-style-for-version: 0.67.46 -> :localgroup" (eq (cli-style-for-version "autolith version 0.67.46") :localgroup))
(check "cli-style-for-version: 0.67.47 -> :top-level" (eq (cli-style-for-version "autolith version 0.67.47") :top-level))
(check "cli-style-for-version: 0.67.66 -> :top-level" (eq (cli-style-for-version "autolith version 0.67.66") :top-level))
(check "cli-style-for-version: 0.68.0 -> :top-level" (eq (cli-style-for-version "autolith version 0.68.0") :top-level))
(check "cli-style-for-version: unparseable defaults to :top-level" (eq (cli-style-for-version "unknown") :top-level))

;; CLI argv builders for both forms, including -- before free-text prompts starting with - or --
(check "run-job-args" (equal (run-job-args "sandbox" "/tmp/j.sexp" "/tmp/r.sexp")
                             '("--permissions" "sandbox" "run-job" "--input" "/tmp/j.sexp" "--output" "/tmp/r.sexp" "--events" "sexp")))
(check "detached-args without model" (equal (detached-args nil) '("--detached" "--permissions" "auto")))
(check "detached-args with model" (equal (detached-args "copilot/claude-sonnet-5.5") '("--detached" "--permissions" "auto" "--model" "copilot/claude-sonnet-5.5")))
(check "status-args top-level" (equal (status-args :top-level) '("status" "--sexp")))
(check "status-args localgroup" (equal (status-args :localgroup) '("localgroup" "status" "--sexp")))
(check "tell-args top-level puts -- before target and prompt" (equal (tell-args "32XYLzs" "hello" :top-level) '("tell" "--" "32XYLzs" "hello")))
(check "tell-args top-level protects prompt starting with -" (equal (tell-args "32XYLzs" "- check foo" :top-level) '("tell" "--" "32XYLzs" "- check foo")))
(check "tell-args top-level protects prompt starting with --" (equal (tell-args "32XYLzs" "--help" :top-level) '("tell" "--" "32XYLzs" "--help")))
(check "tell-args localgroup puts -- before target and prompt" (equal (tell-args "32XYLzs" "--help" :localgroup) '("localgroup" "tell" "--" "32XYLzs" "--help")))
(check "stop-session-args top-level uses pause --" (equal (stop-session-args "32XYLzs" :top-level) '("pause" "--" "32XYLzs")))
(check "stop-session-args localgroup uses localgroup kill --" (equal (stop-session-args "32XYLzs" :localgroup) '("localgroup" "kill" "--" "32XYLzs")))
(check "scripted-session-args without model" (equal (scripted-session-args nil) '("--permissions" "auto")))
(check "scripted-session-args with model" (equal (scripted-session-args "stub/stub-1") '("--permissions" "auto" "--model" "stub/stub-1")))
(check "extract-script-answer strips echoed prompt line"
       (equal (extract-script-answer (format nil "~C -- check~%Hello from Autolith~%" (code-char #x203a))) "Hello from Autolith"))
(check "extract-script-answer keeps tool-marker and answer"
       (equal (extract-script-answer (format nil "~C hi~%* self.redefine~%Done.~%" (code-char #x203a))) (format nil "* self.redefine~%Done.")))
(check "extract-script-answer returns nil on empty" (null (extract-script-answer "   ")))

(format t "~:[all checks passed~;~:*~D check(s) failed~]~%" (and (plusp *failures*) *failures*))
(sb-ext:exit :code (if (zerop *failures*) 0 1))
