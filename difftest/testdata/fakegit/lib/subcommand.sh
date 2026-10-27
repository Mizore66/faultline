# Sourced by the fake gits (`. "$here/../lib/subcommand.sh"`): sets sub to
# git's subcommand, the first word of "$@" that is not a global option.
# The Git verifier runs `git init ...` and `git -C <dir> ...`; the hardened
# runner (git-materialization.ts:115-122) puts -c pairs, --git-dir=<dir>,
# --work-tree=<dir> and -C <dir> first. The space-separated --git-dir,
# --work-tree and --namespace forms take their value as the next word too.
sub=
_skip=
for _a do
  if [ -n "$_skip" ]; then _skip=; continue; fi
  case $_a in
    -c|-C|--git-dir|--work-tree|--namespace) _skip=1 ;;
    -*) ;;
    *) sub=$_a; break ;;
  esac
done
unset _a _skip
