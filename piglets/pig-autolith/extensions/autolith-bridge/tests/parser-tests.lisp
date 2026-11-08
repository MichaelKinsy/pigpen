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

(format t "~:[all checks passed~;~:*~D check(s) failed~]~%" (and (plusp *failures*) *failures*))
(sb-ext:exit :code (if (zerop *failures*) 0 1))
