require 'tempfile'

# One-line examples whose generated descriptions embed a Tempfile name, as in
# omniai's spec/omniai/chat/file_spec.rb.
RSpec.describe 'a temporary file' do
  let(:io) { Tempfile.new }

  it { expect(File.basename(io.path)).to eql(File.basename(io.path)) }
end
