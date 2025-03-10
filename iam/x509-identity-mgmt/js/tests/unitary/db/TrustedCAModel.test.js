const TrustedCAModel = require('../../../src/db/TrustedCAModel');

describe("Unit tests of script 'TrustedCAModel.js'", () => {
  let trustedCAModel = null;
  let mongoClient = null;

  beforeAll(() => {
    mongoClient = {
      parseProjectionFields: jest.fn(),
      sanitizeFields: jest.fn(),
    };
    trustedCAModel = new TrustedCAModel({ mongoClient });
  });

  afterEach(() => {
    jest.clearAllMocks();
  });

  it('should parse condition fields', () => {
    const d = new Date();
    const conditionFields = {
      caFingerprint: '$AB:CD',
      createdAt: d,
    };
    const result = trustedCAModel.parseConditionFields(conditionFields);
    expect(result).toEqual({
      caFingerprint: {
        $options: 'i',
        $regex: 'AB:CD$',
      },
      createdAt: d.toString(),
    });
  });

  it('should parse projection fields', () => {
    const commaSeparatedFields = 'validity,subjectDN';
    trustedCAModel.parseProjectionFields(commaSeparatedFields);
    expect(mongoClient.parseProjectionFields).toHaveBeenCalledTimes(1);
    expect(mongoClient.parseProjectionFields.mock.calls[0][0]).toBe(commaSeparatedFields);
  });

  it('this should sanitize (in depth) object attributes', () => {
    const cert = {
      caFingerprint: null,
      caPem: null,
    };
    trustedCAModel.sanitizeFields(cert);
    expect(mongoClient.sanitizeFields).toHaveBeenCalledTimes(1);
    expect(mongoClient.sanitizeFields.mock.calls[0][0]).toEqual(cert);
  });

  it('should throw an exception because it is a invalid conditional field', () => {
    const conditionFields = {
      caFingerprint: { toString: null },
    };
    expect(() => {
      trustedCAModel.parseConditionFields(conditionFields);
    }).toThrow();
  });

  it('should parse sortBy fields', () => {
    const sortByCreatedAt = 'createdAt';
    const sortByCreatedAtAsc = 'asc:createdAt';
    const sortByCreatedAtDesc = 'desc:createdAt';

    expect(trustedCAModel.parseSortBy(sortByCreatedAt)).toEqual({
      createdAt: 'asc',
    });

    expect(trustedCAModel.parseSortBy(sortByCreatedAtAsc)).toEqual({
      createdAt: 'asc',
    });

    expect(trustedCAModel.parseSortBy(sortByCreatedAtDesc)).toEqual({
      createdAt: 'desc',
    });
  });

  it('should return null if cannot parse sortBy', () => {
    const sortBy = 'field-that-does-not-exist';
    expect(trustedCAModel.parseSortBy(sortBy)).toBe(null);
    expect(trustedCAModel.parseSortBy(123456)).toBe(null);
  });
});
